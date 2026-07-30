package filester

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// downloadTokenHandler returns a deterministic token payload for whatever
// file_slug is requested, optionally pointing "server" back at serverURL
// (mirroring the real /v2/api/public/download response shape).
func downloadTokenHandler(serverURL string, includeServer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			FileSlug string `json:"file_slug"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)

		resp := map[string]string{
			"file":  "file-" + body.FileSlug,
			"token": "tok-" + body.FileSlug,
			"name":  body.FileSlug + "-name.bin",
		}
		if includeServer {
			resp["server"] = serverURL
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func TestParseFilesterFolder(t *testing.T) {
	mux := http.NewServeMux()

	html := `<html><body>
		<div class="file-item" data-name="video1.mp4" data-size="1000" onclick="window.location.href='/d/slugA'"></div>
		<div class="file-item" data-name="image1.jpg" data-size="2000" onclick="window.location.href='/d/slugB'"></div>
	</body></html>`

	mux.HandleFunc("/f/testid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	})

	var ts *httptest.Server
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		downloadTokenHandler(ts.URL, true)(w, r)
	})

	ts = httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewFilesterScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/f/testid")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "video1.mp4" {
		t.Errorf("expected title video1.mp4, got %s", items[0].Title)
	}
	if items[0].Size != 1000 {
		t.Errorf("expected size 1000, got %d", items[0].Size)
	}
	wantURL := fmt.Sprintf("%s/v2/file-slugA?token=tok-slugA&download=true&n=video1.mp4", ts.URL)
	if items[0].URL != wantURL {
		t.Errorf("expected URL %s, got %s", wantURL, items[0].URL)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}

	if items[1].Title != "image1.jpg" {
		t.Errorf("expected title image1.jpg, got %s", items[1].Title)
	}
	if items[1].Type != model.MediaTypeImage {
		t.Errorf("expected image type, got %s", items[1].Type)
	}
}

func TestParseFilesterFolderPagination(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/f/multi", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p2.jpg" data-size="50" onclick="window.location.href='/d/p2'"></div>
			</body></html>`))
			return
		}
		w.Write([]byte(`<html><body>
			<button id="loadAllPagesBtn" data-total="2">Load all pages</button>
			<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
		</body></html>`))
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(ts.URL + "/f/multi")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items across 2 pages, got %d", len(items))
	}
	if items[0].Title != "p1.jpg" || items[1].Title != "p2.jpg" {
		t.Errorf("expected p1.jpg then p2.jpg, got %s then %s", items[0].Title, items[1].Title)
	}
}

// TestParseFilesterFolderRecoversFromTransientEmptyPage guards against a
// real bug: a page that transiently comes back with zero items (rate
// limiting, a momentary blip) used to be treated as "end of the album",
// silently truncating large albums partway through. The first attempt at
// page 2 here returns nothing; the retry should recover it.
func TestParseFilesterFolderRecoversFromTransientEmptyPage(t *testing.T) {
	var page2Attempts int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/flaky", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("page") {
		case "2":
			if atomic.AddInt32(&page2Attempts, 1) < 2 {
				w.Write([]byte(`<html><body></body></html>`)) // transient blip
				return
			}
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p2.jpg" data-size="50" onclick="window.location.href='/d/p2'"></div>
			</body></html>`))
		case "3":
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p3.jpg" data-size="60" onclick="window.location.href='/d/p3'"></div>
			</body></html>`))
		default:
			w.Write([]byte(`<html><body>
				<button id="loadAllPagesBtn" data-total="3">Load all pages</button>
				<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
			</body></html>`))
		}
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(ts.URL + "/f/flaky")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items (page 2 recovers on retry), got %d", len(items))
	}
}

// TestParseFilesterFolderSkipsPermanentlyEmptyPage ensures a page that
// never recovers (even after retries) is skipped rather than aborting the
// rest of the album — pages after it should still be collected.
func TestParseFilesterFolderSkipsPermanentlyEmptyPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/f/partial", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`<html><body></body></html>`)) // always empty
		case "3":
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p3.jpg" data-size="60" onclick="window.location.href='/d/p3'"></div>
			</body></html>`))
		default:
			w.Write([]byte(`<html><body>
				<button id="loadAllPagesBtn" data-total="3">Load all pages</button>
				<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
			</body></html>`))
		}
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(ts.URL + "/f/partial")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items (page 3 still collected despite page 2 permanently failing), got %d", len(items))
	}
}

func TestParseFilesterSingleFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(ts.URL + "/d/Sw9EGcH")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Sw9EGcH-name.bin" {
		t.Errorf("unexpected title: %s", items[0].Title)
	}
	wantURL := fmt.Sprintf("%s/v2/file-Sw9EGcH?token=tok-Sw9EGcH&download=true&n=Sw9EGcH-name.bin", ts.URL)
	if items[0].URL != wantURL {
		t.Errorf("expected URL %s, got %s", wantURL, items[0].URL)
	}
}

func TestParseFilesterFolderNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := NewFilesterScraper()
	scraper.baseURL = ts.URL

	if _, err := scraper.Parse(ts.URL + "/f/missing"); err == nil {
		t.Error("expected error for missing folder")
	}
}

func TestParseFilesterUnrecognizedURL(t *testing.T) {
	scraper := NewFilesterScraper()
	if _, err := scraper.Parse("https://filester.sh/faq"); err == nil {
		t.Error("expected error for unrecognized URL format")
	}
}

func TestExtractKindAndID(t *testing.T) {
	tests := []struct {
		url      string
		wantKind string
		wantID   string
	}{
		{"https://filester.sh/f/473d3487215583bb", "f", "473d3487215583bb"},
		{"https://filester.sh/f/473d3487215583bb/", "f", "473d3487215583bb"},
		{"https://filester.me/d/L7edMh1", "d", "L7edMh1"},
		{"https://filester.sh/d/L7edMh1?utm=1", "d", "L7edMh1"},
		{"https://filester.sh/", "", ""},
		{"https://filester.sh/faq", "", ""},
	}

	for _, tt := range tests {
		kind, id := extractKindAndID(tt.url)
		if kind != tt.wantKind || id != tt.wantID {
			t.Errorf("extractKindAndID(%s) = (%s, %s), want (%s, %s)", tt.url, kind, id, tt.wantKind, tt.wantID)
		}
	}
}

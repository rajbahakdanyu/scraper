package turbo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

func newTestScraper() *Scraper {
	scraper := NewTurboScraper()
	scraper.client = &http.Client{}
	return scraper
}

func TestParseAlbum(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a/test123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><table><tbody id="fileTbody">
<tr class="file-row" data-id="VB8aLAF" data-name="video one.mp4" data-size="310462282"></tr>
<tr class="file-row" data-id="bKXbHrZ" data-name="video two.mp4" data-size="355170059"></tr>
</tbody></table></body></html>`))
	})

	mux.HandleFunc("/api/sign", func(w http.ResponseWriter, r *http.Request) {
		fileID := r.URL.Query().Get("v")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"url":     fmt.Sprintf("https://cdn.turbo.cr/%s.mp4?exp=9999&token=test", fileID),
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/test123")
	if err != nil {
		t.Fatalf("Parse album failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "video one.mp4" {
		t.Errorf("expected title video one.mp4, got %q", items[0].Title)
	}
	if items[0].Size != 310462282 {
		t.Errorf("expected size 310462282, got %d", items[0].Size)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}
	if items[0].URL != ts.URL+"/d/VB8aLAF" {
		t.Errorf("expected page URL, got %s", items[0].URL)
	}
	if items[0].Resolve == nil {
		t.Fatal("expected Resolve func on album items")
	}

	resolved, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !strings.Contains(resolved, "cdn.turbo.cr/VB8aLAF.mp4") {
		t.Errorf("expected CDN URL with file ID, got %s", resolved)
	}
}

func TestParseTurboRejectsOversizedHTML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 33<<20)+`<tr class="file-row" data-id="late" data-name="late.mp4" data-size="1"></tr>`)
	}))
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), ts.URL+"/a/oversized")
	if err == nil {
		t.Fatal("expected error for oversized album page")
	}
}

func TestParseTurboRejectsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), ts.URL+"/a/missing")
	if err == nil {
		t.Fatal("expected error for 404 album page")
	}
	var statusErr *netutil.HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected *netutil.HTTPStatusError, got %T: %v", err, err)
	}
}

func TestParseSingleFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/d/abc123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
<div id="fileName">my video.mp4</div>
<span id="fileSizeBytes">100,000</span>
</body></html>`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/d/abc123")
	if err != nil {
		t.Fatalf("Parse single file failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "my video.mp4" {
		t.Errorf("expected title my video.mp4, got %q", items[0].Title)
	}
	if items[0].Size != 100000 {
		t.Errorf("expected size 100000, got %d", items[0].Size)
	}
	if items[0].Resolve == nil {
		t.Fatal("expected Resolve func on file item")
	}
}

func TestParseEmptyAlbum(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><table><tbody id="fileTbody"></tbody></table></body></html>`))
	}))
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), ts.URL+"/a/empty")
	if err == nil {
		t.Fatal("expected error for empty album")
	}
}

func TestParseSignFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><table><tbody id="fileTbody">
<tr class="file-row" data-id="abc123" data-name="test.mp4" data-size="100"></tr>
</tbody></table></body></html>`))
	})

	mux.HandleFunc("/api/sign", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/test")
	if err != nil {
		t.Fatalf("Parse should succeed, resolve should fail: %v", err)
	}

	_, err = items[0].Resolve(context.Background())
	if err == nil {
		t.Fatal("expected resolve failure for unsuccessful sign")
	}
}

func TestTurboResolve404IsPermanentNotRetried(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a/album", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><table><tbody id="fileTbody">
<tr class="file-row" data-id="gone" data-name="gone.mp4" data-size="100"></tr>
</tbody></table></body></html>`))
	})
	mux.HandleFunc("/api/sign", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/album")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	_, err = items[0].Resolve(context.Background())
	var permanent *model.PermanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("expected a PermanentError for a 404 sign response, got %T: %v", err, err)
	}
}

func TestExtractID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://turbo.cr/a/g2UEE4mQilU", "g2UEE4mQilU"},
		{"https://turbo.cr/d/VB8aLAFdShZPw", "VB8aLAFdShZPw"},
		{"https://turbo.cr/v/VB8aLAFdShZPw", "VB8aLAFdShZPw"},
		{"https://turbo.cr/a/g2UEE4mQilU?ref=1", "g2UEE4mQilU"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := extractID(tt.url); got != tt.want {
				t.Errorf("extractID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestScraperName(t *testing.T) {
	s := newTestScraper()
	if s.Name() != "turbo" {
		t.Errorf("expected name turbo, got %s", s.Name())
	}
}

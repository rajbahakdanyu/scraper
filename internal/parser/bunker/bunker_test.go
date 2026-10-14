// internal/parser/bunker/bunker_test.go
package bunker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestParseFilePageSkipsDecoyDownloadAnchors(t *testing.T) {
	const page = `<!doctype html><html><head><title>v.mp4 | Bunkr</title></head><body>
		<a href="https://evil.example/file/1">related</a>
		<a href="https://dl.bunkr.cr/file/61567794">Get your file</a>
		</body></html>`
	mux := http.NewServeMux()
	mux.HandleFunc("/f/abc123", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestBunkerScraper()
	items, err := scraper.Parse(context.Background(), ts.URL+"/f/abc123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].URL != "https://dl.bunkr.cr/file/61567794" {
		t.Fatalf("a decoy anchor must never become the download/API host, got %s", items[0].URL)
	}
}

func TestParseBunkerRejectsOversizedHTML(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 32<<20)+`<a href="/f/file" aria-label="download"></a>`)
	})
	mux.HandleFunc("/f/file", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<title>file.bin | Bunkr</title><a href="/file/123">Download</a>`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestBunkerScraper()
	items, err := scraper.Parse(context.Background(), ts.URL)
	if err == nil || len(items) != 0 {
		t.Fatalf("expected link truncated by HTML limit, items=%d err=%v", len(items), err)
	}
}

// TestResolve404IsPermanentNotRetried: when the signing API 404s (the file
// is gone upstream), the resolver surfaces a model.NonRetryableError — the
// classification the downloader uses to fail the item immediately — and
// the API sees exactly one request. The downloader half is covered by the
// downloader's TestDownloadPermanentResolverErrorFailsImmediately.
func TestResolve404IsPermanentNotRetried(t *testing.T) {
	var apiRequests int32

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/a/album", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="/f/gone" aria-label="download"></a></body></html>`)
	})
	mux.HandleFunc("/f/gone", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>gone.mp4 | Bunkr</title></head><body>
<a href="%s/file/61567794">Download</a></body></html>`, ts.URL)
	})
	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&apiRequests, 1)
		w.WriteHeader(http.StatusNotFound)
	})

	scraper := newTestBunkerScraper()

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/album")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	_, err = items[0].Resolve(context.Background())
	if err == nil {
		t.Fatal("expected resolver error for a deleted file")
	}
	var pe *model.NonRetryableError
	if !errors.As(err, &pe) {
		t.Errorf("expected a model.NonRetryableError so the downloader fails fast, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&apiRequests); got != 1 {
		t.Errorf("expected exactly 1 API request (no retries), got %d", got)
	}
}

func TestGetSignedMediaURLRejectsNonOKSigningResponse(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"mediafiles": "https://cdn.example.com",
			"path":       "/storage/media/test.mp4",
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token": "must-not-be-used",
			"ex":    9999999999,
		})
	})

	scraper := newTestBunkerScraper()
	scraper.signURL = ts.URL + "/sign"
	dlURL, err := url.Parse(ts.URL + "/file/123")
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := scraper.getSignedMediaURL(context.Background(), dlURL, "123", "test.mp4")
	if err == nil || !strings.Contains(err.Error(), "HTTP 201") {
		t.Fatalf("expected signing response error containing HTTP 201, got %v", err)
	}
	if resolved != "" {
		t.Fatalf("resolved URL = %q, want empty URL on signing failure", resolved)
	}
}

func TestResolveSigningServerErrorIsNotDownloaderForbidden(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/a/album", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><a href="/f/file" aria-label="download"></a></body></html>`)
	})
	mux.HandleFunc("/f/file", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><head><title>file.mp4 | Bunkr</title></head><body><a href="%s/file/123">Download</a></body></html>`, ts.URL)
	})
	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"mediafiles": "https://cdn.example.com",
			"path":       "/storage/media/file.mp4",
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	scraper := newTestBunkerScraper()
	scraper.signURL = ts.URL + "/sign"
	items, err := scraper.Parse(context.Background(), ts.URL+"/a/album")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	resolved, err := items[0].Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("expected resolver error containing HTTP 500, got %v", err)
	}
	if strings.Contains(err.Error(), "403") {
		t.Fatalf("resolver error incorrectly reported downloader 403: %v", err)
	}
	if resolved != "" {
		t.Fatalf("resolved URL = %q, want empty URL on signing failure", resolved)
	}
}

// Regression test: a file ID containing characters that need JSON escaping
// must produce a valid JSON API request body with the exact ID value.
func TestGetSignedMediaURLEscapesFileID(t *testing.T) {
	var body []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"mediafiles": "https://cdn.example.com",
			"path":       "/storage/media/test.mp4",
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token": "testtoken123",
			"ex":    9999999999,
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestBunkerScraper()
	scraper.signURL = ts.URL + "/sign"

	fileID := `6156"7794\x`
	dlURL, err := url.Parse(ts.URL + "/file/" + fileID)
	if err != nil {
		t.Fatalf("failed to parse dl URL: %v", err)
	}

	if _, err := scraper.getSignedMediaURL(context.Background(), dlURL, fileID, "test.mp4"); err != nil {
		t.Fatalf("getSignedMediaURL failed: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("API request body is not valid JSON: %v (body: %q)", err, body)
	}
	if decoded["id"] != fileID {
		t.Errorf("expected id %q in request body, got %q", fileID, decoded["id"])
	}
}

func TestParseBunkerSingleFile(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/f/singlefile", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>test video.mp4 | Bunkr</title></head><body>
<a href="https://bunkr.cr/file/61567">Download</a></body></html>`)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestBunkerScraper()

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/singlefile")
	if err != nil {
		t.Fatalf("Parse single file /f/ID failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "test video.mp4" {
		t.Errorf("expected title 'test video.mp4', got %q", items[0].Title)
	}
	if items[0].Resolve == nil {
		t.Fatal("expected Resolve func for lazy signed-URL resolution")
	}
}

func TestParseBunkerSingleFileNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestBunkerScraper()

	_, err := scraper.Parse(context.Background(), ts.URL+"/f/missing")
	if err == nil {
		t.Error("expected error for missing single file")
	}
}

func TestBunkerFileIDIgnoresHrefQueryString(t *testing.T) {
	var apiBody struct {
		ID string `json:"id"`
	}

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/a/album", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<a href="/f/one" aria-label="download"></a>`)
	})
	mux.HandleFunc("/f/one", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>q.bin | Bunkr</title></head><body>
<a href="%s/file/61567794?download=1">Download</a></body></html>`, ts.URL)
	})
	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&apiBody)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"mediafiles": "https://cdn.example.com",
			"path":       "/storage/media/q.bin",
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"token": "tok", "ex": 9999999999})
	})

	scraper := newTestBunkerScraper()
	scraper.signURL = ts.URL + "/sign"

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/album")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if _, err := items[0].Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if apiBody.ID != "61567794" {
		t.Fatalf("API file id = %q, want 61567794", apiBody.ID)
	}
}

func TestBunkerSignResponseBodyIsBounded(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/a/album", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<a href="/f/one" aria-label="download"></a>`)
	})
	mux.HandleFunc("/f/one", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>x.bin | Bunkr</title></head><body>
<a href="%s/file/1">Download</a></body></html>`, ts.URL)
	})
	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"mediafiles": "https://cdn.example.com",
			"path":       "/media/x.bin",
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"token":"`)
		_, _ = io.WriteString(w, strings.Repeat("a", 10<<20))
		_, _ = io.WriteString(w, `","ex":9999999999}`)
	})

	scraper := newTestBunkerScraper()
	scraper.signURL = ts.URL + "/sign"

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/album")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if _, err := items[0].Resolve(context.Background()); err == nil {
		t.Fatal("expected an oversized signing response to fail decoding")
	}
}

func TestParseBunkerRecordsPageURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/f/pageurl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>clip.mp4 | Bunkr</title></head><body>
<a href="https://bunkr.cr/file/61567">Download</a></body></html>`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestBunkerScraper()
	items, err := scraper.Parse(context.Background(), ts.URL+"/f/pageurl")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].PageURL != ts.URL+"/f/pageurl" {
		t.Errorf("PageURL = %q, want %q", items[0].PageURL, ts.URL+"/f/pageurl")
	}
}

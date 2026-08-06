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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func newTestBunkerScraper() *Scraper {
	scraper := NewBunkerScraper()
	scraper.client = &http.Client{}
	return scraper
}

func TestParseBunkerRejectsOversizedHTML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 32<<20+1))
	}))
	defer ts.Close()

	scraper := newTestBunkerScraper()
	scraper.baseURL = ts.URL
	if _, err := scraper.Parse(ts.URL); err == nil {
		t.Fatal("expected oversized HTML to fail parsing")
	}
}

func TestParseBunkerAlbum(t *testing.T) {
	var apiRequests, signRequests int32

	mux := http.NewServeMux()

	// Album page
	albumHTML := `<html><body>
<a href="/f/LJiSwHaGgRq2i" aria-label="download" target="_blank"></a>
<a href="/f/tQqpEH85Y8tjT" aria-label="download" target="_blank"></a>
</body></html>`

	mux.HandleFunc("/a/test123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(albumHTML))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// File page links to the mock dl server (needs ts.URL, so set after ts created)
	mux.HandleFunc("/f/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Jamie Lo (39).mp4 | Bunkr</title></head><body>
<a href="%s/file/61567794">Download</a></body></html>`, ts.URL)
	})
	// Mock dl.bunkr.cr API
	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&apiRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"mediafiles": "https://cdn.example.com",
			"path":       "/storage/media/test.mp4",
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&signRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token": "testtoken123",
			"ex":    9999999999,
		})
	})

	scraper := newTestBunkerScraper()
	scraper.baseURL = ts.URL
	scraper.signURL = ts.URL + "/sign"
	items, err := scraper.Parse(ts.URL + "/a/test123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "Jamie Lo (39).mp4" {
		t.Errorf("expected title 'Jamie Lo (39).mp4', got %s", items[0].Title)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}

	// The signed CDN URL (token + expiry) is NOT resolved during Parse —
	// the item carries the stable dl.bunkr URL and a resolver instead.
	if got := atomic.LoadInt32(&apiRequests); got != 0 {
		t.Errorf("Parse hit the signing API %d times eagerly; resolution must be lazy", got)
	}
	if got := atomic.LoadInt32(&signRequests); got != 0 {
		t.Errorf("Parse requested %d signatures eagerly; resolution must be lazy", got)
	}
	if items[0].URL != ts.URL+"/file/61567794" {
		t.Errorf("expected the stable dl URL until download time, got %s", items[0].URL)
	}
	if items[0].Resolve == nil {
		t.Fatal("expected a Resolve func for lazy signed-URL resolution")
	}

	resolved, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !strings.Contains(resolved, "cdn.example.com/storage/media/test.mp4") {
		t.Errorf("expected CDN URL containing cdn.example.com, got %s", resolved)
	}
	if !strings.Contains(resolved, "token=testtoken123") {
		t.Errorf("expected signed URL to carry the signature token, got %s", resolved)
	}
	if got := atomic.LoadInt32(&apiRequests); got != 1 {
		t.Errorf("expected 1 API request after Resolve, got %d", got)
	}
	if got := atomic.LoadInt32(&signRequests); got != 1 {
		t.Errorf("expected 1 signing request after Resolve, got %d", got)
	}
}

// TestResolve404IsPermanentNotRetried: when the signing API 404s (the file
// is gone upstream), the resolver surfaces a model.PermanentError — the
// classification the downloader uses to fail the item immediately — and
// the API sees exactly one request. The downloader half (a PermanentError
// fails the download with no retries) is covered by the downloader's
// TestDownloadPermanentResolverErrorFailsImmediately.
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
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/album")
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
	var pe *model.PermanentError
	if !errors.As(err, &pe) {
		t.Errorf("expected a model.PermanentError so the downloader fails fast, got %T: %v", err, err)
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
	scraper.baseURL = ts.URL
	scraper.signURL = ts.URL + "/sign"
	dlURL, err := url.Parse(ts.URL + "/file/123")
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := scraper.getSignedMediaURL(context.Background(), dlURL, "123", "test.mp4")
	if err != nil {
		t.Fatalf("getSignedMediaURL failed: %v", err)
	}
	if want := "https://cdn.example.com/storage/media/test.mp4"; resolved != want {
		t.Fatalf("resolved URL = %q, want unsigned URL %q", resolved, want)
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

// Regression test: hrefs found on the album page must be resolved relative
// to the page URL — protocol-relative (//host/path), absolute-path (/path),
// relative (path) and absolute hrefs all resolve to the correct URL.
func TestParseBunkerAlbumHrefResolution(t *testing.T) {
	tests := []struct {
		name     string
		tls      bool
		href     func(tsURL string) string
		wantPath string
	}{
		{
			name: "protocol-relative href on http page",
			href: func(tsURL string) string {
				return "//" + strings.TrimPrefix(tsURL, "http://") + "/f/protorel"
			},
			wantPath: "/f/protorel",
		},
		{
			name: "protocol-relative href on https page",
			tls:  true,
			href: func(tsURL string) string {
				return "//" + strings.TrimPrefix(tsURL, "https://") + "/f/protorel"
			},
			wantPath: "/f/protorel",
		},
		{
			name:     "absolute path href",
			href:     func(string) string { return "/f/abspath" },
			wantPath: "/f/abspath",
		},
		{
			name:     "relative path href",
			href:     func(string) string { return "f/relpath" },
			wantPath: "/a/f/relpath",
		},
		{
			name:     "absolute URL href",
			href:     func(tsURL string) string { return tsURL + "/f/absurl" },
			wantPath: "/f/absurl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var filePaths []string

			var ts *httptest.Server
			mux := http.NewServeMux()
			mux.HandleFunc("/a/album", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, `<html><body><a href="%s" aria-label="download"></a></body></html>`, tt.href(ts.URL))
			})
			mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
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
			// Catch-all serves the file page for any resolved file URL and
			// records the request path so the test can verify resolution.
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				filePaths = append(filePaths, r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, `<html><head><title>test.mp4 | Bunkr</title></head><body>
<a href="%s/file/61567794">Download</a></body></html>`, ts.URL)
			})

			if tt.tls {
				ts = httptest.NewTLSServer(mux)
			} else {
				ts = httptest.NewServer(mux)
			}
			defer ts.Close()

			scraper := newTestBunkerScraper()
			scraper.baseURL = ts.URL
			scraper.signURL = ts.URL + "/sign"
			if tt.tls {
				scraper.client = ts.Client()
			}

			items, err := scraper.Parse(ts.URL + "/a/album")
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(items))
			}
			resolved, err := items[0].Resolve(context.Background())
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			if !strings.Contains(resolved, "cdn.example.com/storage/media/test.mp4") {
				t.Errorf("expected CDN URL containing cdn.example.com, got %s", resolved)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(filePaths) != 1 || filePaths[0] != tt.wantPath {
				t.Errorf("expected file page request path %q, got %v", tt.wantPath, filePaths)
			}
		})
	}
}

func TestParseBunkerAlbumNoLinks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>no files here</body></html>`))
	}))
	defer ts.Close()

	scraper := newTestBunkerScraper()
	scraper.baseURL = ts.URL
	_, err := scraper.Parse(ts.URL + "/a/test")
	if err == nil {
		t.Error("expected error for album with no file links")
	}
}

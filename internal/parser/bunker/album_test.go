package bunker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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
	scraper.signURL = ts.URL + "/sign"
	items, err := scraper.Parse(context.Background(), ts.URL+"/a/test123")
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
			scraper.signURL = ts.URL + "/sign"
			if tt.tls {
				scraper.client = ts.Client()
			}

			items, err := scraper.Parse(context.Background(), ts.URL+"/a/album")
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

func TestParseBunkerAlbumJSOnlyPageGetsClearError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script>
			if (window.albumFiles && Array.isArray(window.albumFiles)) { console.log('Advanced mode'); }
		</script></body></html>`))
	}))
	defer ts.Close()

	scraper := newTestBunkerScraper()
	_, err := scraper.Parse(context.Background(), ts.URL+"/a/jsalbum")
	if err == nil {
		t.Fatal("expected an error for a JS-only album page")
	}
	if !strings.Contains(err.Error(), "JavaScript") || !strings.Contains(err.Error(), "browser") {
		t.Fatalf("expected a JS-rendered explanation, got: %v", err)
	}
}

func TestParseBunkerAlbumNoLinks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>no files here</body></html>`))
	}))
	defer ts.Close()

	scraper := newTestBunkerScraper()
	_, err := scraper.Parse(context.Background(), ts.URL+"/a/test")
	if err == nil {
		t.Error("expected error for album with no file links")
	}
}

func TestParseBunkerAlbumErrorIncludesURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>no files here</body></html>`))
	}))
	defer ts.Close()

	scraper := newTestBunkerScraper()
	albumURL := ts.URL + "/a/empty"
	_, err := scraper.Parse(context.Background(), albumURL)
	if err == nil {
		t.Fatal("expected error for album with no file links")
	}
	if !strings.Contains(err.Error(), albumURL) {
		t.Fatalf("error must name the failing album page, got %q", err.Error())
	}
}

func TestParseBunkerAlbumRetriesTransientEmptyPage(t *testing.T) {
	var albumRequests int32

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/a/flaky", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if atomic.AddInt32(&albumRequests, 1) == 1 {
			_, _ = io.WriteString(w, `<html><body>challenge interstitial</body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<html><body><a href="/f/one" aria-label="download"></a></body></html>`)
	})
	mux.HandleFunc("/f/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>x.mp4 | Bunkr</title></head><body>
<a href="%s/file/1">Download</a></body></html>`, ts.URL)
	})

	scraper := newTestBunkerScraper()

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/flaky")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after the retry, got %d", len(items))
	}
	if got := atomic.LoadInt32(&albumRequests); got != 2 {
		t.Fatalf("expected 2 album requests (one retry), got %d", got)
	}
}

func TestParseBunkerAlbumRetriesTransientStatus(t *testing.T) {
	var albumRequests int32

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/a/ratelimited", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&albumRequests, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><body><a href="/f/one" aria-label="download"></a></body></html>`)
	})
	mux.HandleFunc("/f/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>x.mp4 | Bunkr</title></head><body>
<a href="%s/file/1">Download</a></body></html>`, ts.URL)
	})

	scraper := newTestBunkerScraper()

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/ratelimited")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after the retry, got %d", len(items))
	}
	if got := atomic.LoadInt32(&albumRequests); got != 2 {
		t.Fatalf("expected 2 album requests (one retry), got %d", got)
	}
}

func TestParseBunkerAlbumPermanentErrorNotRetried(t *testing.T) {
	var albumRequests int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&albumRequests, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestBunkerScraper()

	if _, err := scraper.Parse(context.Background(), ts.URL+"/a/gone"); err == nil {
		t.Fatal("expected a 404 album to fail")
	}
	if got := atomic.LoadInt32(&albumRequests); got != 1 {
		t.Fatalf("permanent 404 must not be retried, got %d requests", got)
	}
}

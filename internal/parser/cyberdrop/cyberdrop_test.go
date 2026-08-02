// internal/parser/cyberdrop/cyberdrop_test.go
package cyberdrop

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func newTestCyberDropScraper() *Scraper {
	scraper := NewCyberDropScraper()
	scraper.client = &http.Client{}
	return scraper
}

// TestParseFile exercises the real Parse() -> parseFile() code path end to
// end against an httptest server (via the injectable baseURL field), rather
// than duplicating the HTTP calls inline and discarding the result.
func TestParseFile(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/file/info/T5LPyd5Sa9HkZ", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":     "test_video.mp4",
			"type":     "video/mp4",
			"size":     float64(7861080),
			"auth_url": ts.URL + "/file/auth/T5LPyd5Sa9HkZ",
		})
	})
	mux.HandleFunc("/file/auth/T5LPyd5Sa9HkZ", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url": "https://cdn.example.com/d/T5LPyd5Sa9HkZ?token=abc123",
		})
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/f/T5LPyd5Sa9HkZ")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "test_video.mp4" {
		t.Errorf("expected title test_video.mp4, got %s", items[0].Title)
	}
	if items[0].URL != "https://cdn.example.com/d/T5LPyd5Sa9HkZ?token=abc123" {
		t.Errorf("expected CDN URL, got %s", items[0].URL)
	}
	if items[0].Size != 7861080 {
		t.Errorf("expected size 7861080, got %d", items[0].Size)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}
}

func TestParseFileNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(ts.URL + "/f/missing")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseFileRejectsMalformedAuthURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "file.mp4", "auth_url": "://bad-auth-url",
		})
	}))
	defer ts.Close()

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL
	if _, err := scraper.Parse(ts.URL + "/f/bad-auth"); err == nil {
		t.Fatal("expected malformed auth_url error")
	}
}

func TestParseFileRejectsPrivateAuthURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "file.mp4", "auth_url": "http://169.254.169.254/latest/meta-data/",
		})
	}))
	defer ts.Close()

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL
	if _, err := scraper.Parse(ts.URL + "/f/private-auth"); err == nil {
		t.Fatal("expected private auth_url to be rejected")
	}
}

// TestParseAlbum exercises Parse() -> parseAlbum() -> parseFile() (once per
// album entry) end to end against an httptest server.
func TestParseAlbum(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/album/abc123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"files": []map[string]interface{}{
				{"name": "one.mp4", "slug": "slug1", "size": float64(100), "type": "video/mp4"},
				{"name": "two.jpg", "slug": "slug2", "size": float64(200), "type": "image/jpeg"},
			},
		})
	})
	mux.HandleFunc("/file/info/slug1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "one.mp4", "type": "video/mp4", "size": float64(100),
			"auth_url": ts.URL + "/file/auth/slug1",
		})
	})
	mux.HandleFunc("/file/auth/slug1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://cdn.example.com/one.mp4"})
	})
	mux.HandleFunc("/file/info/slug2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "two.jpg", "type": "image/jpeg", "size": float64(200),
			"auth_url": ts.URL + "/file/auth/slug2",
		})
	})
	mux.HandleFunc("/file/auth/slug2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://cdn.example.com/two.jpg"})
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/abc123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "one.mp4" || items[0].URL != "https://cdn.example.com/one.mp4" {
		t.Errorf("unexpected item 0: %+v", items[0])
	}
	if items[1].Title != "two.jpg" || items[1].Size != 200 {
		t.Errorf("unexpected item 1: %+v", items[1])
	}
}

// TestParseAlbumResolvesFilesConcurrently observes at the fake API server
// that per-file resolutions overlap in flight (more than one at a time) and
// never exceed the package's concurrency bound.
func TestParseAlbumResolvesFilesConcurrently(t *testing.T) {
	const numFiles = maxConcurrentResolutions + 2

	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/album/big", func(w http.ResponseWriter, r *http.Request) {
		files := make([]map[string]interface{}, 0, numFiles)
		for i := 0; i < numFiles; i++ {
			files = append(files, map[string]interface{}{
				"name": fmt.Sprintf("file%02d.mp4", i),
				"slug": fmt.Sprintf("slug%02d", i),
				"size": float64(100 + i),
				"type": "video/mp4",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"files": files})
	})
	mux.HandleFunc("/file/info/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		// Hold the request open so overlapping resolutions are observable.
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()

		slug := strings.TrimPrefix(r.URL.Path, "/file/info/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name": slug + ".mp4", "type": "video/mp4", "size": float64(100),
			"auth_url": ts.URL + "/file/auth/" + slug,
		})
	})
	mux.HandleFunc("/file/auth/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/file/auth/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://cdn.example.com/" + slug + ".mp4"})
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/big")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != numFiles {
		t.Fatalf("expected %d items, got %d", numFiles, len(items))
	}

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight <= 1 {
		t.Errorf("expected concurrent resolution, max in-flight was %d", maxInFlight)
	}
	if maxInFlight > maxConcurrentResolutions {
		t.Errorf("max in-flight %d exceeded bound %d", maxInFlight, maxConcurrentResolutions)
	}
}

// TestParseAlbumPreservesOrderWithOutOfOrderCompletion makes later album
// entries resolve faster than earlier ones, so results assembled by
// completion order would come back reversed; album order must win.
func TestParseAlbumPreservesOrderWithOutOfOrderCompletion(t *testing.T) {
	delays := map[string]time.Duration{
		"slow": 150 * time.Millisecond,
		"mid":  75 * time.Millisecond,
		"fast": 0,
	}

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/album/ordered", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"files": []map[string]interface{}{
				{"name": "slow.mp4", "slug": "slow", "size": float64(300), "type": "video/mp4"},
				{"name": "mid.mp4", "slug": "mid", "size": float64(200), "type": "video/mp4"},
				{"name": "fast.mp4", "slug": "fast", "size": float64(100), "type": "video/mp4"},
			},
		})
	})
	mux.HandleFunc("/file/info/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/file/info/")
		time.Sleep(delays[slug])
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name": slug + ".mp4", "type": "video/mp4", "size": float64(100),
			"auth_url": ts.URL + "/file/auth/" + slug,
		})
	})
	mux.HandleFunc("/file/auth/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/file/auth/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://cdn.example.com/" + slug + ".mp4"})
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/ordered")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	wantTitles := []string{"slow.mp4", "mid.mp4", "fast.mp4"}
	for i, want := range wantTitles {
		if items[i].Title != want {
			t.Errorf("item %d: expected title %s, got %s", i, want, items[i].Title)
		}
	}
}

// TestParseAlbumPartialFailure keeps the existing semantics: one failed
// resolution does not abort the album — the good items come back in order
// and the failures surface as a PartialError.
func TestParseAlbumPartialFailure(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/album/mixed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"files": []map[string]interface{}{
				{"name": "one.mp4", "slug": "ok1", "size": float64(100), "type": "video/mp4"},
				{"name": "bad.mp4", "slug": "bad", "size": float64(200), "type": "video/mp4"},
				{"name": "two.mp4", "slug": "ok2", "size": float64(300), "type": "video/mp4"},
			},
		})
	})
	mux.HandleFunc("/file/info/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/file/info/")
		if slug == "bad" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name": slug + ".mp4", "type": "video/mp4", "size": float64(100),
			"auth_url": ts.URL + "/file/auth/" + slug,
		})
	})
	mux.HandleFunc("/file/auth/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/file/auth/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://cdn.example.com/" + slug + ".mp4"})
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/mixed")
	var partialErr *model.PartialError
	if !errors.As(err, &partialErr) {
		t.Fatalf("expected partial-result warning, got: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 resolved items, got %d", len(items))
	}
	if items[0].Title != "one.mp4" || items[1].Title != "two.mp4" {
		t.Errorf("expected surviving items in album order, got %s then %s", items[0].Title, items[1].Title)
	}
	if len(partialErr.Errors) != 1 {
		t.Errorf("expected 1 resolution failure counted, got %d", len(partialErr.Errors))
	}
}

// TestParseAlbumAllResolutionsFail keeps the existing semantics: when no
// file resolves, the album fails hard with no items.
func TestParseAlbumAllResolutionsFail(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/album/dead", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"files": []map[string]interface{}{
				{"name": "one.mp4", "slug": "bad1", "size": float64(100), "type": "video/mp4"},
				{"name": "two.mp4", "slug": "bad2", "size": float64(200), "type": "video/mp4"},
			},
		})
	})
	mux.HandleFunc("/file/info/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/dead")
	if err == nil {
		t.Fatal("expected error when no album file resolves")
	}
	if len(items) != 0 {
		t.Errorf("expected no items, got %d", len(items))
	}
	if !strings.Contains(err.Error(), "could not resolve any files") {
		t.Errorf("expected hard-failure message, got: %v", err)
	}
}

func TestExtractSlug(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://cyberdrop.cr/f/T5LPyd5Sa9HkZ", "T5LPyd5Sa9HkZ"},
		{"https://cyberdrop.cr/a/abc123", "abc123"},
		{"https://cyberdrop.cr/f/test/", "test"},
		{"https://cyberdrop.cr/f/abc123?ref=xyz", "abc123"},
		{"https://cyberdrop.cr/f/abc123/?ref=xyz", "abc123"},
	}

	for _, tt := range tests {
		got := extractSlug(tt.url)
		if got != tt.expected {
			t.Errorf("extractSlug(%s) = %s, want %s", tt.url, got, tt.expected)
		}
	}
}

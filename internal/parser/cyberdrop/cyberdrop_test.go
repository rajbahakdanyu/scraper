// internal/parser/cyberdrop/cyberdrop_test.go
package cyberdrop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func newTestCyberDropScraper() *Scraper {
	scraper := NewCyberDropScraper()
	scraper.client = &http.Client{}
	return scraper
}

// TestParseFile exercises the real Parse() -> parseFile() code path end to
// end against an httptest server (via the injectable baseURL field), rather
// than duplicating the HTTP calls inline and discarding the result. The
// signed URL exchange is lazy: Parse fetches only the file's metadata, and
// the item's Resolve func performs the auth exchange at download time.
func TestParseFile(t *testing.T) {
	var authRequests int32

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
		atomic.AddInt32(&authRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url": "https://cdn.example.com/d/T5LPyd5Sa9HkZ?token=abc123",
		})
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/T5LPyd5Sa9HkZ")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "test_video.mp4" {
		t.Errorf("expected title test_video.mp4, got %s", items[0].Title)
	}
	if items[0].Size != 7861080 {
		t.Errorf("expected size 7861080, got %d", items[0].Size)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}
	// The signed CDN URL is NOT resolved during Parse — the item carries
	// the stable page URL and a resolver instead.
	if got := atomic.LoadInt32(&authRequests); got != 0 {
		t.Errorf("Parse resolved the signed URL eagerly (%d auth requests); resolution must be lazy", got)
	}
	if items[0].URL != ts.URL+"/f/T5LPyd5Sa9HkZ" {
		t.Errorf("expected the stable page URL until download time, got %s", items[0].URL)
	}
	if items[0].Resolve == nil {
		t.Fatal("expected a Resolve func for lazy signed-URL resolution")
	}

	resolved, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolved != "https://cdn.example.com/d/T5LPyd5Sa9HkZ?token=abc123" {
		t.Errorf("expected CDN URL from Resolve, got %s", resolved)
	}
	if got := atomic.LoadInt32(&authRequests); got != 1 {
		t.Errorf("expected 1 auth request after Resolve, got %d", got)
	}
	// The downloader re-resolves after a 403, so Resolve must be safely
	// repeatable.
	if _, err := items[0].Resolve(context.Background()); err != nil {
		t.Fatalf("second Resolve failed: %v", err)
	}
	if got := atomic.LoadInt32(&authRequests); got != 2 {
		t.Errorf("expected 2 auth requests after re-resolve, got %d", got)
	}
}

func TestParseFileNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), ts.URL+"/f/missing")
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
	if _, err := scraper.Parse(context.Background(), ts.URL+"/f/bad-auth"); err == nil {
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
	if _, err := scraper.Parse(context.Background(), ts.URL+"/f/private-auth"); err == nil {
		t.Fatal("expected private auth_url to be rejected")
	}
}

// TestParseAlbum exercises Parse() -> parseAlbum() end to end against an
// httptest server. Album parsing is lazy: items are built straight from
// the listing metadata with zero per-file requests, and each item's
// Resolve func performs that file's signed-URL exchange at download time.
func TestParseAlbum(t *testing.T) {
	var perFileRequests int32

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
	mux.HandleFunc("/file/info/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&perFileRequests, 1)
		slug := strings.TrimPrefix(r.URL.Path, "/file/info/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name": slug + ".mp4", "type": "video/mp4", "size": float64(100),
			"auth_url": ts.URL + "/file/auth/" + slug,
		})
	})
	mux.HandleFunc("/file/auth/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&perFileRequests, 1)
		slug := strings.TrimPrefix(r.URL.Path, "/file/auth/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://cdn.example.com/" + slug + ".mp4"})
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/abc123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if got := atomic.LoadInt32(&perFileRequests); got != 0 {
		t.Errorf("Parse made %d per-file requests; album resolution must be lazy", got)
	}
	// Metadata comes from the album listing; URLs stay the stable page
	// URLs until download time.
	if items[0].Title != "one.mp4" || items[0].URL != ts.URL+"/f/slug1" {
		t.Errorf("unexpected item 0: %+v", items[0])
	}
	if items[1].Title != "two.jpg" || items[1].Size != 200 || items[1].URL != ts.URL+"/f/slug2" {
		t.Errorf("unexpected item 1: %+v", items[1])
	}

	// Resolvers perform the signed-URL exchange on demand.
	resolved0, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolved0 != "https://cdn.example.com/slug1.mp4" {
		t.Errorf("expected signed CDN URL for slug1, got %s", resolved0)
	}
	resolved1, err := items[1].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolved1 != "https://cdn.example.com/slug2.mp4" {
		t.Errorf("expected signed CDN URL for slug2, got %s", resolved1)
	}
	if got := atomic.LoadInt32(&perFileRequests); got != 4 {
		t.Errorf("expected 4 per-file requests after resolving both items (2 info + 2 auth), got %d", got)
	}
}

// TestParseAlbumMakesNoPerFileRequests: parsing even a large album is a
// single listing request — the per-file resolution fan-out moved to
// download time, where the downloader's own concurrency bound applies.
func TestParseAlbumMakesNoPerFileRequests(t *testing.T) {
	const numFiles = 12
	var perFileRequests int32

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
	mux.HandleFunc("/file/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&perFileRequests, 1)
		w.WriteHeader(http.StatusNotFound)
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/big")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != numFiles {
		t.Fatalf("expected %d items, got %d", numFiles, len(items))
	}
	for i, item := range items {
		if item.Title != fmt.Sprintf("file%02d.mp4", i) {
			t.Errorf("item %d: expected title file%02d.mp4 in album order, got %s", i, i, item.Title)
		}
	}
	if got := atomic.LoadInt32(&perFileRequests); got != 0 {
		t.Errorf("Parse made %d per-file requests for a %d-file album; resolution must be lazy", got, numFiles)
	}
}

// TestParseAlbumDefersResolutionFailures documents the lazy-resolution
// semantic shift: Parse can no longer see per-file resolution failures, so
// every listed file becomes an item (in album order, no PartialError) and
// a bad file only fails when its resolver runs at download time.
func TestParseAlbumDefersResolutionFailures(t *testing.T) {
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

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/mixed")
	if err != nil {
		t.Fatalf("lazy Parse must not fail on deferred resolution errors, got: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected all 3 listed files as items, got %d", len(items))
	}
	wantTitles := []string{"one.mp4", "bad.mp4", "two.mp4"}
	for i, want := range wantTitles {
		if items[i].Title != want {
			t.Errorf("item %d: expected title %s in album order, got %s", i, want, items[i].Title)
		}
	}
	if _, err := items[1].Resolve(context.Background()); err == nil {
		t.Error("expected the bad file's resolver to fail at download time")
	}
	if _, err := items[0].Resolve(context.Background()); err != nil {
		t.Errorf("good file's resolver failed: %v", err)
	}
	if _, err := items[2].Resolve(context.Background()); err != nil {
		t.Errorf("good file's resolver failed: %v", err)
	}
}

// TestResolve404IsPermanentNotRetried: when the resolve API 404s (the file
// is gone upstream), the resolver surfaces a model.PermanentError — the
// classification the downloader uses to fail the item immediately — and
// the resolve API sees exactly one request. The downloader half (a
// PermanentError fails the download with no retries) is covered by
// TestDownloadPermanentResolverErrorFailsImmediately.
func TestResolve404IsPermanentNotRetried(t *testing.T) {
	var infoRequests int32

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/album/gone", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"files": []map[string]interface{}{
				{"name": "gone.mp4", "slug": "gone", "size": float64(100), "type": "video/mp4"},
			},
		})
	})
	mux.HandleFunc("/file/info/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&infoRequests, 1)
		w.WriteHeader(http.StatusNotFound)
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/gone")
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
	if got := atomic.LoadInt32(&infoRequests); got != 1 {
		t.Errorf("expected exactly 1 resolve API request (no retries), got %d", got)
	}
}

// TestResolve500IsTransientNotPermanent: a 5xx from the resolve API is a
// transient failure — it must NOT be wrapped as permanent, so the
// downloader keeps its retry-with-backoff behavior for it.
func TestResolve500IsTransientNotPermanent(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	mux.HandleFunc("/album/blip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"files": []map[string]interface{}{
				{"name": "blip.mp4", "slug": "blip", "size": float64(100), "type": "video/mp4"},
			},
		})
	})
	mux.HandleFunc("/file/info/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/blip")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	_, err = items[0].Resolve(context.Background())
	if err == nil {
		t.Fatal("expected resolver error for a 500ing resolve API")
	}
	var pe *model.PermanentError
	if errors.As(err, &pe) {
		t.Errorf("a transient 500 must not be classified permanent, got %T: %v", err, err)
	}
}

// TestParseAlbumResolversAllFail: when every file is gone upstream, Parse
// still returns the listed items — the failures surface per item when the
// resolvers run at download time, not as a hard Parse error.
func TestParseAlbumResolversAllFail(t *testing.T) {
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

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/dead")
	if err != nil {
		t.Fatalf("lazy Parse returns listed items without resolving them, got: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for i := range items {
		if _, err := items[i].Resolve(context.Background()); err == nil {
			t.Errorf("expected item %d's resolver to fail for a deleted file", i)
		}
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

func TestParseFileRejectsUnlistedAuthHost(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "f.mp4", "size": 1,
			"auth_url": "https://evil.example.net/file/auth/x",
		})
	}))
	defer ts.Close()

	scraper := newTestCyberDropScraper()
	scraper.baseURL = ts.URL

	if _, err := scraper.Parse(context.Background(), ts.URL+"/f/slug"); err == nil ||
		!strings.Contains(err.Error(), "unsafe auth_url") {
		t.Fatalf("error = %v, want unsafe auth_url", err)
	}
}

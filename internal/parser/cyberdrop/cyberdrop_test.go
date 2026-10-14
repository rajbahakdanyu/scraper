// internal/parser/cyberdrop/cyberdrop_test.go
package cyberdrop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

// TestResolve404IsPermanentNotRetried: when the resolve API 404s (the file
// is gone upstream), the resolver surfaces a model.NonRetryableError — the
// classification the downloader uses to fail the item immediately — and
// the resolve API sees exactly one request. The downloader half is covered
// by TestDownloadPermanentResolverErrorFailsImmediately.
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
	var pe *model.NonRetryableError
	if !errors.As(err, &pe) {
		t.Errorf("expected a model.NonRetryableError so the downloader fails fast, got %T: %v", err, err)
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
	var pe *model.NonRetryableError
	if errors.As(err, &pe) {
		t.Errorf("a transient 500 must not be classified permanent, got %T: %v", err, err)
	}
}

func TestParseFileRejectsUnlistedAuthRedirect(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:9/other", http.StatusFound)
	}))
	defer auth.Close()

	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "f.mp4", "size": 1,
			"auth_url": auth.URL,
		})
	}))
	defer info.Close()

	scraper := newTestCyberDropScraper()
	scraper.baseURL = info.URL

	items, err := scraper.Parse(context.Background(), info.URL+"/f/slug")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_, err = items[0].Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("resolve error = %v, want an allowlist rejection for the redirect", err)
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

// internal/parser/goonbox/goonbox_test.go
package goonbox

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
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func newTestGoonBoxScraper() *Scraper {
	scraper := NewGoonBoxScraper()
	scraper.client = &http.Client{}
	return scraper
}

// TestParseGoonBoxAlbum exercises the real Parse() code path end to end
// against an httptest server (via the injectable baseURL field), rather
// than duplicating the HTTP call inline and discarding the result.
func TestParseGoonBoxAlbum(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/albums/testid", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"album": map[string]string{"title": "Test Album"},
				"images": []map[string]interface{}{
					{"original_url": "https://cdn.example.com/img1.jpg", "original_filename": "img1.jpg", "size_bytes": float64(1024), "mime": "image/jpeg"},
					{"original_url": "https://cdn.example.com/img2.jpg", "original_filename": "img2.jpg", "size_bytes": float64(2048), "mime": "image/jpeg"},
				},
				"pagination": map[string]int{"current_page": 1, "last_page": 1},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/testid")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "img1.jpg" {
		t.Errorf("expected title img1.jpg, got %s", items[0].Title)
	}
	if items[0].URL != "https://cdn.example.com/img1.jpg" {
		t.Errorf("expected image URL, got %s", items[0].URL)
	}
	if items[0].Type != model.MediaTypeImage {
		t.Errorf("expected image type, got %s", items[0].Type)
	}
	if items[1].Size != 2048 {
		t.Errorf("expected size 2048, got %d", items[1].Size)
	}
	// The image CDN rejects hotlinks: every item must carry the site
	// origin as its Referer.
	if items[0].Referer != siteOrigin {
		t.Errorf("expected referer %q, got %q", siteOrigin, items[0].Referer)
	}
	if items[1].Referer != siteOrigin {
		t.Errorf("expected referer %q, got %q", siteOrigin, items[1].Referer)
	}
}

func TestParseGoonBoxAlbumNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), ts.URL+"/a/missing")
	if err == nil {
		t.Error("expected error for missing album")
	}
}

func TestParseGoonBoxRejectsMalformedRequestURL(t *testing.T) {
	scraper := newTestGoonBoxScraper()
	scraper.baseURL = "://bad-base-url"

	if _, err := scraper.Parse(context.Background(), "https://goonbox.cr/a/testid"); err == nil {
		t.Fatal("expected malformed request URL error")
	}
}

func TestParseGoonBoxRejectsPrivateImageURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"images": []map[string]interface{}{{
				"original_url": "http://127.0.0.1/private.jpg", "original_filename": "private.jpg",
			}},
			"pagination": map[string]int{"current_page": 1, "last_page": 1},
		})
	}))
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL
	if _, err := scraper.Parse(context.Background(), ts.URL+"/a/private"); err == nil {
		t.Fatal("expected private image URL to be rejected")
	}
}

// TestParseGoonBoxAlbumPagination exercises the page-follow loop in Parse(),
// which previously had no test coverage at all.
func TestParseGoonBoxAlbumPagination(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/albums/multi", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"album": map[string]string{"title": "Multi"},
				"images": []map[string]interface{}{
					{"original_url": "https://cdn.example.com/p2.jpg", "original_filename": "p2.jpg", "size_bytes": float64(50), "mime": "image/jpeg"},
				},
				"pagination": map[string]int{"current_page": 2, "last_page": 2},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"album": map[string]string{"title": "Multi"},
			"images": []map[string]interface{}{
				{"original_url": "https://cdn.example.com/p1.jpg", "original_filename": "p1.jpg", "size_bytes": float64(40), "mime": "image/jpeg"},
			},
			"pagination": map[string]int{"current_page": 1, "last_page": 2},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/multi")
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

func TestParseGoonBoxAlbumParallelPagination(t *testing.T) {
	var requests int32
	var active int32
	var maxActive int32
	mux := http.NewServeMux()
	mux.HandleFunc("/albums/parallel", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		n := 0
		_, _ = fmt.Sscan(page, &n)
		atomic.AddInt32(&requests, 1)
		current := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if current <= old || atomic.CompareAndSwapInt32(&maxActive, old, current) {
				break
			}
		}
		defer atomic.AddInt32(&active, -1)
		if n == 1 {
			time.Sleep(40 * time.Millisecond)
		} else {
			time.Sleep(time.Duration(6-n) * 5 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"images": []map[string]interface{}{{
				"original_url":      fmt.Sprintf("https://cdn.example.com/p%d.jpg", n),
				"original_filename": fmt.Sprintf("p%d.jpg", n),
			}},
			"pagination": map[string]int{"current_page": n, "last_page": 5},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL
	items, err := scraper.Parse(context.Background(), ts.URL+"/a/parallel")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 5 {
		t.Fatalf("expected 5 page requests, got %d", got)
	}
	if got := atomic.LoadInt32(&maxActive); got < 2 {
		t.Fatalf("expected concurrent page requests, max active was %d", got)
	}
	for i, item := range items {
		want := fmt.Sprintf("p%d.jpg", i+1)
		if item.Title != want {
			t.Fatalf("item %d = %q, want %q", i, item.Title, want)
		}
	}
}

// TestParseGoonBoxAlbumPaginationCapped ensures a hostile API that always
// claims another page exists cannot keep Parse fetching forever: Parse
// stops at maxAlbumPages requests and returns the items gathered so far
// with no error.
func TestParseGoonBoxAlbumUnderreportedLastPageKeepsTail(t *testing.T) {
	var requests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/albums/under", func(w http.ResponseWriter, r *http.Request) {
		n := 0
		_, _ = fmt.Sscan(r.URL.Query().Get("page"), &n)
		atomic.AddInt32(&requests, 1)
		last := 2 // every page after the first under-reports
		if n == 1 {
			last = 12
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"images": []map[string]interface{}{{
				"original_url":      fmt.Sprintf("https://cdn.example.com/p%d.jpg", n),
				"original_filename": fmt.Sprintf("p%d.jpg", n),
			}},
			"pagination": map[string]int{"current_page": n, "last_page": last},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL
	items, err := scraper.Parse(context.Background(), ts.URL+"/a/under")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 12 {
		t.Fatalf("under-reported last_page must not drop the album tail: got %d items, want 12", len(items))
	}
	if got := atomic.LoadInt32(&requests); got != 12 {
		t.Fatalf("expected 12 page requests, got %d", got)
	}
}

func TestParseGoonBoxAlbumPaginationCapped(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		if n > maxAlbumPages {
			// Sentinel: fails the test fast instead of hanging if the
			// pagination cap regresses.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"images": []map[string]interface{}{{
				"original_url":      fmt.Sprintf("https://cdn.example.com/img%d.jpg", n),
				"original_filename": fmt.Sprintf("img%d.jpg", n),
			}},
			"pagination": map[string]int{"current_page": int(n), "last_page": int(n) + 1},
		})
	}))
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/hostile")
	if err != nil {
		t.Fatalf("expected no error when the page cap is hit, got: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != maxAlbumPages {
		t.Errorf("expected exactly %d requests (the cap), got %d", maxAlbumPages, got)
	}
	if len(items) != maxAlbumPages {
		t.Errorf("expected %d items collected before the cap, got %d", maxAlbumPages, len(items))
	}
}

// TestParseGoonBoxAlbumSkipsUnsafeImageURLs ensures one unsafe entry does
// not abort the whole album: safe images are still returned, and the skips
// surface as a PartialError warning (the pattern cyberdrop/filester/bunker
// already use for partial success).
func TestParseGoonBoxAlbumSkipsUnsafeImageURLs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"album": map[string]string{"title": "Mixed"},
			"images": []map[string]interface{}{
				{"original_url": "https://cdn.example.com/safe1.jpg", "original_filename": "safe1.jpg", "size_bytes": float64(100), "mime": "image/jpeg"},
				{"original_url": "http://127.0.0.1/secret.jpg", "original_filename": "secret.jpg", "size_bytes": float64(200), "mime": "image/jpeg"},
				{"original_url": "http://192.168.1.1/internal.jpg", "original_filename": "internal.jpg", "size_bytes": float64(300), "mime": "image/jpeg"},
				{"original_url": "https://cdn.example.com/safe2.jpg", "original_filename": "safe2.jpg", "size_bytes": float64(400), "mime": "image/jpeg"},
			},
			"pagination": map[string]int{"current_page": 1, "last_page": 1},
		})
	}))
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/mixed")
	var partialErr *model.PartialError
	if !errors.As(err, &partialErr) {
		t.Fatalf("expected partial-result warning, got: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 safe items, got %d", len(items))
	}
	if items[0].URL != "https://cdn.example.com/safe1.jpg" || items[1].URL != "https://cdn.example.com/safe2.jpg" {
		t.Errorf("expected only the safe URLs in order, got %s then %s", items[0].URL, items[1].URL)
	}
	if len(partialErr.Errors) != 2 {
		t.Errorf("expected 2 skipped unsafe items counted, got %d", len(partialErr.Errors))
	}
}

// TestParseGoonBoxAlbumAllUnsafeImageURLs ensures an album where every image
// is unsafe still fails (landing on the existing "no images found" error)
// rather than reporting an empty success.
func TestParseGoonBoxAlbumAllUnsafeImageURLs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"images": []map[string]interface{}{
				{"original_url": "http://127.0.0.1/a.jpg", "original_filename": "a.jpg"},
				{"original_url": "http://10.0.0.5/b.jpg", "original_filename": "b.jpg"},
			},
			"pagination": map[string]int{"current_page": 1, "last_page": 1},
		})
	}))
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/allunsafe")
	if err == nil {
		t.Fatal("expected error when every image URL is unsafe")
	}
	if len(items) != 0 {
		t.Errorf("expected no items, got %d", len(items))
	}
	if !strings.Contains(err.Error(), "no images found") {
		t.Errorf("expected all-skipped album to land on the no-images-found error, got: %v", err)
	}
}

func TestParseGoonBoxAlbumSurfacesPageErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/albums/testid", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"album":      map[string]string{"title": "Test Album"},
				"images":     []map[string]interface{}{{"original_url": "https://cdn.example.com/img1.jpg", "original_filename": "img1.jpg", "size_bytes": float64(1024), "mime": "image/jpeg"}},
				"pagination": map[string]int{"current_page": 1, "last_page": 2},
			})
			return
		}
		// Page 2: permanent server error.
		w.WriteHeader(http.StatusInternalServerError)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/testid")
	if len(items) != 1 {
		t.Fatalf("expected page-1 items to survive, got %d", len(items))
	}
	var partial *model.PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("expected a PartialError naming the failed page, got %v", err)
	}
}

func TestExtractAlbumID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://goonbox.cr/a/YbtJKH", "YbtJKH"},
		{"https://goonbox.cr/a/YbtJKH/", "YbtJKH"},
		{"https://goonbox.cr/a/abc123", "abc123"},
		{"https://goonbox.cr/a/YbtJKH?utm=1", "YbtJKH"},
	}

	for _, tt := range tests {
		got := extractAlbumID(tt.url)
		if got != tt.expected {
			t.Errorf("extractAlbumID(%s) = %s, want %s", tt.url, got, tt.expected)
		}
	}
}

func TestParseGoonBoxAlbumKeepsPagesFetchedBeforeShrink(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/albums/shrink", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		last := 4
		if page == "2" {
			last = 2 // hostile/inconsistent: claims the album ends here
		}
		n := 0
		_, _ = fmt.Sscan(page, &n)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"images": []map[string]interface{}{{
				"original_url":      fmt.Sprintf("https://cdn.example.com/p%d.jpg", n),
				"original_filename": fmt.Sprintf("p%d.jpg", n),
			}},
			"pagination": map[string]int{"current_page": n, "last_page": last},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/a/shrink")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("expected all 4 fetched pages kept, got %d items", len(items))
	}
	for i, item := range items {
		want := fmt.Sprintf("p%d.jpg", i+1)
		if item.Title != want {
			t.Fatalf("item %d = %q, want %q", i, item.Title, want)
		}
	}
}

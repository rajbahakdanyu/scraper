package pixeldrain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

type noopSleeper struct{}

func (noopSleeper) Sleep(time.Duration) {}

func (noopSleeper) SleepCtx(context.Context, time.Duration) error { return nil }

func TestParseListRetriesTransientFailure(t *testing.T) {
	var attempts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/list/retrylist", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"title":   "list",
			"files": []map[string]interface{}{
				{"id": "abc", "name": "video.mp4", "size": 123},
			},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL
	scraper.SetSleeper(noopSleeper{})

	items, err := scraper.Parse(context.Background(), "https://pixeldrain.com/l/retrylist")
	if err != nil {
		t.Fatalf("Parse should recover from a transient 500: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
}

func TestParseListPermanentErrorNotRetried(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL
	scraper.SetSleeper(noopSleeper{})

	if _, err := scraper.Parse(context.Background(), "https://pixeldrain.com/l/missing"); err == nil {
		t.Fatal("expected a 404 to fail")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (404 is permanent)", got)
	}
}

func newTestScraper() *Scraper {
	scraper := NewPixeldrainScraper()
	scraper.client = &http.Client{}
	return scraper
}

func TestParseList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/list/CSbjLYKs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"id":         "CSbjLYKs",
			"title":      "Tingle bay",
			"file_count": 3,
			"files": []map[string]interface{}{
				{
					"id":          "qZz2NiiW",
					"name":        "vid 9.mp4",
					"size":        float64(341367083),
					"mime_type":   "video/mp4",
					"hash_sha256": "794ce9c4",
				},
				{
					"id":          "2DGrEbrb",
					"name":        "vid 8.mp4",
					"size":        float64(323611323),
					"mime_type":   "video/mp4",
					"hash_sha256": "cc2fe779",
				},
				{
					"id":          "CdBXfu8d",
					"name":        "vid 1.mp4",
					"size":        float64(1206524182),
					"mime_type":   "video/mp4",
					"hash_sha256": "d4d897e7",
				},
			},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), "https://pixeldrain.com/l/CSbjLYKs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	if items[0].Title != "vid 9.mp4" {
		t.Errorf("expected title vid 9.mp4, got %s", items[0].Title)
	}
	if items[0].Size != 341367083 {
		t.Errorf("expected size 341367083, got %d", items[0].Size)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}
	wantURL := ts.URL + "/file/qZz2NiiW"
	if items[0].URL != wantURL {
		t.Errorf("expected download URL %s, got %s", wantURL, items[0].URL)
	}
}

func TestParseListStripsFragment(t *testing.T) {
	mux := http.NewServeMux()
	var called string
	mux.HandleFunc("/list/", func(w http.ResponseWriter, r *http.Request) {
		called = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"id":         "CSbjLYKs",
			"title":      "test",
			"file_count": 1,
			"files": []map[string]interface{}{
				{"id": "abc123", "name": "file.mp4", "size": float64(100), "mime_type": "video/mp4"},
			},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), "https://pixeldrain.com/l/CSbjLYKs#")
	if err != nil {
		t.Fatalf("Parse with fragment failed: %v", err)
	}
	if called != "/list/CSbjLYKs" {
		t.Errorf("expected /list/CSbjLYKs, got %s", called)
	}
}

func TestParseSingleFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/file/qZz2NiiW/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"id":          "qZz2NiiW",
			"name":        "vid 9.mp4",
			"size":        float64(341367083),
			"mime_type":   "video/mp4",
			"hash_sha256": "794ce9c4",
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), "https://pixeldrain.com/u/qZz2NiiW")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "vid 9.mp4" {
		t.Errorf("expected title vid 9.mp4, got %s", items[0].Title)
	}
	wantURL := ts.URL + "/file/qZz2NiiW"
	if items[0].URL != wantURL {
		t.Errorf("expected download URL %s, got %s", wantURL, items[0].URL)
	}
}

func TestParseEmptyList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/list/empty", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"id":         "empty",
			"title":      "Empty List",
			"file_count": 0,
			"files":      []map[string]interface{}{},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), "https://pixeldrain.com/l/empty")
	if err == nil {
		t.Fatal("expected error for empty list")
	}
}

func TestParseAPIFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
		})
	}))
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), "https://pixeldrain.com/l/badlist")
	if err == nil {
		t.Fatal("expected error for API success=false")
	}
}

func TestParseInvalidURL(t *testing.T) {
	scraper := newTestScraper()
	_, err := scraper.Parse(context.Background(), "https://pixeldrain.com/")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestExtractID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://pixeldrain.com/l/CSbjLYKs", "CSbjLYKs"},
		{"https://pixeldrain.com/u/qZz2NiiW", "qZz2NiiW"},
		{"https://pixeldrain.com/l/CSbjLYKs#", "CSbjLYKs"},
		{"https://pixeldrain.com/l/CSbjLYKs?test=1", "CSbjLYKs"},
		{"https://pixeldrain.com/l/CSbjLYKs/?test=1", "CSbjLYKs"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := extractID(tt.url); got != tt.want {
				t.Errorf("extractID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestMediaTypeDetection(t *testing.T) {
	scraper := newTestScraper()
	if scraper.Name() != "pixeldrain" {
		t.Errorf("expected name pixeldrain, got %s", scraper.Name())
	}
}

func TestParseAPIURL(t *testing.T) {
	mux := http.NewServeMux()
	var called string
	mux.HandleFunc("/file/", func(w http.ResponseWriter, r *http.Request) {
		called = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"id":          "qZz2NiiW",
			"name":        "vid 9.mp4",
			"size":        float64(341367083),
			"mime_type":   "video/mp4",
			"hash_sha256": "794ce9c4",
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), "https://pixeldrain.com/api/file/qZz2NiiW?download")
	if err != nil {
		t.Fatalf("Parse /api/file/ID?download failed: %v", err)
	}
	if len(items) != 1 || items[0].Title != "vid 9.mp4" {
		t.Fatalf("expected 1 item with title vid 9.mp4, got %d items", len(items))
	}
	if !strings.Contains(called, "qZz2NiiW") {
		t.Errorf("expected API call for qZz2NiiW, got %s", called)
	}
}

func TestParseDirectFileURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/file/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"id":          "abc123",
			"name":        "test.mp4",
			"size":        float64(100),
			"mime_type":   "video/mp4",
			"hash_sha256": "abcdef",
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), ts.URL+"/file/abc123")
	if err != nil {
		t.Fatalf("Parse /file/ID failed: %v", err)
	}
}

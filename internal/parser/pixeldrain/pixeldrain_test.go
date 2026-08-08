package pixeldrain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

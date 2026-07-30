// internal/parser/goonbox/goonbox_test.go
package goonbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

	scraper := NewGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/testid")
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
}

func TestParseGoonBoxAlbumNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := NewGoonBoxScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(ts.URL + "/a/missing")
	if err == nil {
		t.Error("expected error for missing album")
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

	scraper := NewGoonBoxScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(ts.URL + "/a/multi")
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

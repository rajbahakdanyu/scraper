// internal/parser/cyberdrop/cyberdrop_test.go
package cyberdrop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

	scraper := NewCyberDropScraper()
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

	scraper := NewCyberDropScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(ts.URL + "/f/missing")
	if err == nil {
		t.Error("expected error for missing file")
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

	scraper := NewCyberDropScraper()
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

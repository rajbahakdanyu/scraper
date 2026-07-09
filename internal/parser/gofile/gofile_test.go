// internal/parser/gofile/gofile_test.go
package gofile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseGoFileURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) < 2 {
			t.Error("expected content ID in path")
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"children": map[string]interface{}{
					"abc123": map[string]interface{}{
						"name": "test_video.mp4",
						"link": "https://example.com/test_video.mp4",
						"size": float64(1024),
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	scraper := NewGoFileScraper()
	items, err := scraper.Parse(ts.URL + "/s/abc123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].Title != "test_video.mp4" {
		t.Errorf("expected title test_video.mp4, got %s", items[0].Title)
	}
}

func TestExtractContentID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://gofile.io/s/abc123", "abc123"},
		{"https://gofile.io/v/abc123", "abc123"},
		{"https://gofile.io/c/abc123/details", "details"},
	}

	for _, tt := range tests {
		id := extractContentID(tt.url)
		if id != tt.expected {
			t.Errorf("extractContentID(%s) = %s, want %s", tt.url, id, tt.expected)
		}
	}
}
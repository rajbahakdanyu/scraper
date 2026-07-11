// internal/parser/gofile/gofile_test.go
package gofile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGoFileURL(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]string{
				"token": "testtoken123",
			},
		})
	})

	mux.HandleFunc("/contents/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer testtoken123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"name": "test",
				"children": map[string]interface{}{
					"abc123": map[string]interface{}{
						"name": "test_video.mp4",
						"link": "https://example.com/test_video.mp4",
						"size": float64(1024),
					},
				},
			},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewGoFileScraper()
	scraper.baseURL = ts.URL
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
	if items[0].URL != "https://example.com/test_video.mp4" {
		t.Errorf("expected example.com URL, got %s", items[0].URL)
	}
}

func TestParseGoFilePremiumError(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]string{
				"token": "testtoken123",
			},
		})
	})

	mux.HandleFunc("/contents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error-notPremium",
			"data":   map[string]interface{}{},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewGoFileScraper()
	scraper.baseURL = ts.URL
	_, err := scraper.Parse(ts.URL + "/s/abc123")
	if err == nil {
		t.Fatal("expected error for premium content")
	}
	if !strings.Contains(err.Error(), "premium") {
		t.Errorf("expected premium error message, got: %v", err)
	}
}

func TestExtractContentID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://gofile.io/s/abc123", "abc123"},
		{"https://gofile.io/v/abc123", "abc123"},
		{"https://gofile.io/c/abc123/details", "abc123"},
		{"https://gofile.io/d/xcbupi", "xcbupi"},
	}

	for _, tt := range tests {
		id := extractContentID(tt.url)
		if id != tt.expected {
			t.Errorf("extractContentID(%s) = %s, want %s", tt.url, id, tt.expected)
		}
	}
}

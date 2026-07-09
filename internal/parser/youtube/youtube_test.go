// internal/parser/youtube/youtube_test.go
package youtube

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/scraper/internal/model"
)

func TestParseYouTubeURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("url") == "" {
			t.Errorf("expected url query parameter")
		}
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("expected format=json")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"title": "Test Video Title", "author_name": "TestAuthor"}`))
	}))
	defer ts.Close()

	scraper := newYouTubeScraperWithBase(ts.URL)
	items, err := scraper.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	if !errors.Is(err, ErrStreamExtractionUnsupported) {
		t.Fatalf("expected ErrStreamExtractionUnsupported, got: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].Title != "Test Video Title" {
		t.Errorf("expected title 'Test Video Title', got %s", items[0].Title)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}
	if items[0].URL != "" {
		t.Errorf("expected empty URL (no stream extraction), got %s", items[0].URL)
	}
}

func TestParseYouTubeInvalidURL(t *testing.T) {
	scraper := NewYouTubeScraper()
	_, err := scraper.Parse("https://www.youtube.com/")
	if err == nil {
		t.Fatal("expected error for URL without video ID")
	}
}

func TestExtractVideoID(t *testing.T) {
	scraper := NewYouTubeScraper()

	tests := []struct {
		url      string
		expected string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ?si=abc", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/", ""},
	}

	for _, tt := range tests {
		got := scraper.extractVideoID(tt.url)
		if got != tt.expected {
			t.Errorf("extractVideoID(%s) = %s, want %s", tt.url, got, tt.expected)
		}
	}
}

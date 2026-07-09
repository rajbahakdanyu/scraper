// internal/parser/youtube/youtube_test.go
package youtube

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/scraper/internal/model"
)

func TestParseYouTubeURL(t *testing.T) {
	// Create a mock server that simulates YouTube's oembed endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request has the expected URL parameter
		if r.URL.Query().Get("url") == "" {
			t.Errorf("expected url query parameter")
		}
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("expected format=json")
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"title": "Test Video Title"}`))
	}))
	defer ts.Close()

	// Create scraper with custom client pointing to our test server
	scraper := NewYouTubeScraper()
	
	// We need to override the client to use our test server
	// For now, let's test the extractVideoID function directly
	videoID := scraper.extractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if videoID != "dQw4w9WgXcQ" {
		t.Errorf("expected dQw4w9WgXcQ, got %s", videoID)
	}

	videoID = scraper.extractVideoID("https://youtu.be/dQw4w9WgXcQ")
	if videoID != "dQw4w9WgXcQ" {
		t.Errorf("expected dQw4w9WgXcQ from youtu.be, got %s", videoID)
	}
	
	// Test URL without video ID
	videoID = scraper.extractVideoID("https://www.youtube.com/")
	if videoID != "" {
		t.Errorf("expected empty string, got %s", videoID)
	}
}
// internal/parser/youtube/youtube_test.go
package youtube

import (
	"context"
	"testing"
)

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/watch?v=abc123XYZ_-", "abc123XYZ_-"},
		{"https://www.youtube.com/playlist?list=PL123", ""},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ?si=abc", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/", ""},
		{"https://example.com/notyoutube", ""},
		// Modern URL shapes.
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/live/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://music.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		// Substring matching on "v=" must not false-positive on unrelated
		// parameters that merely contain it.
		{"https://www.youtube.com/watch?iv=dQw4w9WgXcQ", ""},
		{"https://youtu.be/", ""},
		{"https://www.youtube.com/shorts/", ""},
	}

	for _, tt := range tests {
		got := ExtractVideoID(tt.url)
		if got != tt.expected {
			t.Errorf("ExtractVideoID(%s) = %s, want %s", tt.url, got, tt.expected)
		}
	}
}

func TestParseInvalidURL(t *testing.T) {
	scraper := NewYouTubeScraper()
	_, err := scraper.Parse(context.Background(), "https://www.youtube.com/")
	if err == nil {
		t.Fatal("expected error for URL without video ID")
	}
}

func TestExtractPlaylistID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.youtube.com/playlist?list=PLxyz123", "PLxyz123"},
		{"https://www.youtube.com/watch?v=abc&list=PLxyz123", "PLxyz123"},
		{"https://www.youtube.com/watch?v=abc", ""},
		{"https://youtu.be/abc", ""},
		{"not a url", ""},
	}
	for _, tt := range tests {
		if got := ExtractPlaylistID(tt.url); got != tt.want {
			t.Errorf("ExtractPlaylistID(%s) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestVideoWatchURLRoundTripsWithExtractVideoID(t *testing.T) {
	got := ExtractVideoID(videoWatchURL("dQw4w9WgXcQ"))
	if got != "dQw4w9WgXcQ" {
		t.Errorf("videoWatchURL/ExtractVideoID round trip = %q, want %q", got, "dQw4w9WgXcQ")
	}
}

func TestParsePlaylistReturnsPlayableItems(t *testing.T) {
	fake := startFakeYouTube(t, "playlist-video-1", "unused", "unused")
	fake.playlistResponse = playlistJSON(
		playlistEntryJSON("playlist-video-1", "First video"),
		playlistEntryJSON("playlist-video-2", "Second video"),
	)

	scraper := NewYouTubeScraper()
	fake.wire(scraper)
	items, err := scraper.Parse(t.Context(), "https://www.youtube.com/playlist?list=PL1234567890123")
	if err != nil {
		t.Fatalf("Parse playlist: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Parse playlist returned %d items, want 2", len(items))
	}
	if items[0].Title != "First video" || items[0].URL != videoWatchURL("playlist-video-1") {
		t.Errorf("first playlist item = %+v", items[0])
	}
	if items[1].Title != "Second video" || items[1].URL != videoWatchURL("playlist-video-2") {
		t.Errorf("second playlist item = %+v", items[1])
	}
}

func TestParsePlaylistSkipsNullEntries(t *testing.T) {
	fake := startFakeYouTube(t, "playlist-video-3", "unused", "unused")
	fake.playlistResponse = playlistJSON(
		playlistEntryJSON("playlist-video-3", "Playable"),
		`{"playlistVideoRenderer":null}`,
		playlistEntryJSON("playlist-video-4", "Also playable"),
	)

	scraper := NewYouTubeScraper()
	fake.wire(scraper)
	items, err := scraper.Parse(t.Context(), "https://www.youtube.com/playlist?list=PL1234567890123")
	if err != nil {
		t.Fatalf("Parse playlist with null entry: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Parse playlist returned %d items, want 2", len(items))
	}
}

func TestParsePlaylistEmptyReturnsError(t *testing.T) {
	fake := startFakeYouTube(t, "playlist-video-5", "unused", "unused")
	fake.playlistResponse = playlistJSON()

	scraper := NewYouTubeScraper()
	fake.wire(scraper)
	items, err := scraper.Parse(t.Context(), "https://www.youtube.com/playlist?list=PL1234567890123")
	if err == nil {
		t.Fatalf("empty playlist returned %d items without an error", len(items))
	}
}

func TestParsePlaylistTitleFallsBackToURL(t *testing.T) {
	const videoID = "playlist-video-6"
	fake := startFakeYouTube(t, videoID, "unused", "unused")
	fake.playlistResponse = playlistJSON(playlistEntryJSON(videoID, ""))

	scraper := NewYouTubeScraper()
	fake.wire(scraper)
	items, err := scraper.Parse(t.Context(), "https://www.youtube.com/playlist?list=PL1234567890123")
	if err != nil {
		t.Fatalf("Parse playlist: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Parse playlist returned %d items, want 1", len(items))
	}
	if items[0].Title != items[0].URL {
		t.Fatalf("empty title fallback = %q, want URL %q", items[0].Title, items[0].URL)
	}
}

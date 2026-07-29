// internal/parser/youtube/youtube_test.go
package youtube

import (
	"path/filepath"
	"strings"
	"testing"
)

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
		{"https://example.com/notyoutube", ""},
	}

	for _, tt := range tests {
		got := scraper.extractVideoID(tt.url)
		if got != tt.expected {
			t.Errorf("extractVideoID(%s) = %s, want %s", tt.url, got, tt.expected)
		}
	}
}

func TestParseInvalidURL(t *testing.T) {
	scraper := NewYouTubeScraper()
	_, err := scraper.Parse("https://www.youtube.com/")
	if err == nil {
		t.Fatal("expected error for URL without video ID")
	}
}

func TestSafeOutputPathIsAlwaysAbsolute(t *testing.T) {
	// A relative dest ("." is what a default --output resolves to) combined
	// with a title that sanitizes down to something starting with "-" used
	// to produce a relative path ffmpeg's arg parser could misread as a
	// flag instead of the positional output path.
	got := safeOutputPath(".", "-rf --evil-flag")

	if !filepath.IsAbs(got) {
		t.Fatalf("safeOutputPath(%q, %q) = %q, want an absolute path", ".", "-rf --evil-flag", got)
	}
	if strings.HasPrefix(got, "-") {
		t.Fatalf("safeOutputPath(%q, %q) = %q, must not start with '-'", ".", "-rf --evil-flag", got)
	}
}

func TestSafeOutputPathHasMP4Extension(t *testing.T) {
	got := safeOutputPath("/tmp/downloads", "My Video")
	if filepath.Ext(got) != ".mp4" {
		t.Fatalf("safeOutputPath(...) = %q, want a .mp4 extension", got)
	}
	if filepath.Base(got) != "My Video.mp4" {
		t.Fatalf("safeOutputPath(...) base = %q, want %q", filepath.Base(got), "My Video.mp4")
	}
}

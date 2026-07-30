// internal/parser/youtube/youtube_test.go
package youtube

import (
	"path/filepath"
	"strings"
	"testing"

	ytdl "github.com/kkdai/youtube/v2"

	"github.com/user/scraper/internal/model"
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
	s := NewYouTubeScraper()
	got := s.extractVideoID(videoWatchURL("dQw4w9WgXcQ"))
	if got != "dQw4w9WgXcQ" {
		t.Errorf("videoWatchURL/extractVideoID round trip = %q, want %q", got, "dQw4w9WgXcQ")
	}
}

func TestSelectBestFormatsPicksHighestResolution(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 360, Bitrate: 500},
			{Height: 1080, Bitrate: 4000},
			{Height: 720, Bitrate: 2000},
			{Height: 0, Bitrate: 128, AudioChannels: 2}, // audio-only
			{Height: 0, Bitrate: 256, AudioChannels: 2}, // audio-only, higher bitrate
		},
	}
	videoFmt, audioFmt := selectBestFormats(video)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p video format, got %+v", videoFmt)
	}
	if audioFmt == nil || audioFmt.Bitrate != 256 {
		t.Fatalf("expected the 256-bitrate audio format, got %+v", audioFmt)
	}
}

func TestSelectBestFormatsSkipsAudioWhenAlreadyMuxed(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000, AudioChannels: 2}, // already muxed
			{Height: 0, Bitrate: 256, AudioChannels: 2},
		},
	}
	videoFmt, audioFmt := selectBestFormats(video)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p video format, got %+v", videoFmt)
	}
	if audioFmt != nil {
		t.Errorf("expected no separate audio format for an already-muxed stream, got %+v", audioFmt)
	}
}

func TestSelectBestFormatsBreaksHeightTiesByBitrate(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 3000},
			{Height: 1080, Bitrate: 5000},
		},
	}
	videoFmt, _ := selectBestFormats(video)
	if videoFmt == nil || videoFmt.Bitrate != 5000 {
		t.Fatalf("expected the higher-bitrate 1080p format, got %+v", videoFmt)
	}
}

func TestSelectBestFormatsNoFormats(t *testing.T) {
	videoFmt, audioFmt := selectBestFormats(&ytdl.Video{})
	if videoFmt != nil || audioFmt != nil {
		t.Errorf("expected nil, nil for a video with no formats, got %+v, %+v", videoFmt, audioFmt)
	}
}

func TestEstimatedSizeSumsVideoAndAudio(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, ContentLength: 1000},
			{Height: 0, AudioChannels: 2, ContentLength: 200},
		},
	}
	if got := estimatedSize(video); got != 1200 {
		t.Errorf("estimatedSize = %d, want 1200", got)
	}
}

func TestNewDownloaderEnforcesMinConcurrency(t *testing.T) {
	d := NewDownloader(NewYouTubeScraper(), 0)
	if cap(d.sem) != 1 {
		t.Errorf("expected a concurrency floor of 1, got %d", cap(d.sem))
	}
}

func TestDownloaderReturnsErrorForUnresolvableURL(t *testing.T) {
	d := NewDownloader(NewYouTubeScraper(), 1)
	item := model.MediaItem{URL: "not-a-youtube-url"}

	if err := d.Download(item, t.TempDir()); err == nil {
		t.Fatal("expected an error for a URL with no extractable video ID")
	}
}

func TestDownloaderCancelIsSafeForUnknownItem(t *testing.T) {
	d := NewDownloader(NewYouTubeScraper(), 1)
	// Must not panic even though nothing is downloading for this item.
	d.Cancel(model.MediaItem{URL: "https://www.youtube.com/watch?v=xyz"})
}

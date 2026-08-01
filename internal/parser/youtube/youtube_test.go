// internal/parser/youtube/youtube_test.go
package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ytdl "github.com/kkdai/youtube/v2"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
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

func TestSelectBestFormatsPrefersDefaultAudioLanguage(t *testing.T) {
	defaultTrack := &struct {
		DisplayName    string `json:"displayName"`
		ID             string `json:"id"`
		AudioIsDefault bool   `json:"audioIsDefault"`
	}{DisplayName: "English", AudioIsDefault: true}
	nonDefaultTrack := &struct {
		DisplayName    string `json:"displayName"`
		ID             string `json:"id"`
		AudioIsDefault bool   `json:"audioIsDefault"`
	}{DisplayName: "Spanish", AudioIsDefault: false}
	video := &ytdl.Video{Formats: ytdl.FormatList{
		{Height: 1080, Bitrate: 4000},
		{Bitrate: 128, AudioChannels: 2, AudioTrack: defaultTrack},
		{Bitrate: 256, AudioChannels: 2, AudioTrack: nonDefaultTrack},
	}}

	_, audioFmt := selectBestFormats(video)
	if audioFmt == nil || audioFmt.AudioTrack == nil || !audioFmt.AudioTrack.AudioIsDefault {
		t.Fatalf("selected non-default audio: %+v", audioFmt)
	}
}

func TestFormatsForDownloadRejectsMuteVideo(t *testing.T) {
	video := &ytdl.Video{ID: "mute", Formats: ytdl.FormatList{{Height: 1080, Bitrate: 4000}}}
	if _, _, err := formatsForDownload(video); err == nil || !strings.Contains(err.Error(), "no audio format") {
		t.Fatalf("expected no-audio error, got %v", err)
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
	d := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{})
	if cap(d.sem) != 1 {
		t.Errorf("expected a concurrency floor of 1, got %d", cap(d.sem))
	}
}

func TestNewDownloaderWithConfigAppliesTransferPolicy(t *testing.T) {
	d := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{
		Concurrency:  3,
		Timeout:      5 * time.Second,
		UserAgent:    "Custom/1.0",
		RateLimit:    1024,
		SkipChecksum: true,
	})
	if cap(d.sem) != 3 || d.limiter == nil || !d.skipSum {
		t.Fatalf("policy not applied: sem=%d limiter=%v skip=%v", cap(d.sem), d.limiter, d.skipSum)
	}
}

func TestCommitOutputIsAtomicAndWritesChecksum(t *testing.T) {
	dir := t.TempDir()
	tempPath := filepath.Join(dir, "video.part.mp4")
	finalPath := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(tempPath, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := commitOutput(tempPath, finalPath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temporary output still exists: %v", err)
	}
	present, matches, err := fileutil.VerifyChecksum(finalPath)
	if err != nil || !present || !matches {
		t.Fatalf("checksum verification = present %v matches %v err %v", present, matches, err)
	}
}

func TestDownloaderReturnsErrorForUnresolvableURL(t *testing.T) {
	d := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
	item := model.MediaItem{URL: "not-a-youtube-url"}

	if err := d.Download(item, t.TempDir()); err == nil {
		t.Fatal("expected an error for a URL with no extractable video ID")
	}
}

func TestDownloaderCancelIsSafeForUnknownItem(t *testing.T) {
	d := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
	// Must not panic even though nothing is downloading for this item.
	d.Cancel(model.MediaItem{URL: "https://www.youtube.com/watch?v=xyz"})
}

func TestCopyStreamToFileRejectsTruncatedStream(t *testing.T) {
	// The stream ends cleanly (io.EOF) after fewer bytes than the format
	// metadata promised — without a size check this truncated file would be
	// committed and its checksum sidecar would bless the truncation forever.
	short := strings.NewReader("only-partial-content")
	path := filepath.Join(t.TempDir(), "video.mp4")

	err := copyStreamToFile(t.Context(), short, 1<<20, path, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected a size-mismatch error for a truncated stream, got %v", err)
	}
}

func TestCopyStreamToFileAcceptsExactStream(t *testing.T) {
	content := strings.Repeat("x", 4096)
	path := filepath.Join(t.TempDir(), "video.mp4")

	if err := copyStreamToFile(t.Context(), strings.NewReader(content), int64(len(content)), path, nil, nil); err != nil {
		t.Fatalf("unexpected error for an exact-size stream: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("file content = %d bytes, want %d", len(got), len(content))
	}
}

func TestCopyStreamToFileSkipsCheckWhenSizeUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mp4")

	if err := copyStreamToFile(t.Context(), strings.NewReader("whatever"), 0, path, nil, nil); err != nil {
		t.Fatalf("size 0 (unknown) must not trigger the size check, got %v", err)
	}
}

func TestWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	attempts := 0
	err := withRetry(t.Context(), func() error {
		attempts++
		if attempts < 3 {
			return errors.New("transient network blip")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestWithRetryGivesUpAfterMaxAttempts(t *testing.T) {
	attempts := 0
	err := withRetry(t.Context(), func() error {
		attempts++
		return errors.New("always failing")
	})
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if attempts != maxRetries {
		t.Fatalf("attempts = %d, want %d", attempts, maxRetries)
	}
}

func TestWithRetryStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	err := withRetry(ctx, func() error {
		attempts++
		cancel() // first failure cancels the context (user pressed 'c')
		return errors.New("cancelled mid-stream")
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if attempts != 1 {
		t.Fatalf("retry slept through cancellation: attempts = %d, want 1", attempts)
	}
}

// stubFFmpeg puts an executable named "ffmpeg" on PATH that runs the given
// shell script body, so mux code can be tested without a real ffmpeg. The
// stub records its arguments to the file named in $FFMPEG_ARGS_FILE.
func stubFFmpeg(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	full := "#!/bin/sh\n" + `echo "$@" > "$FFMPEG_ARGS_FILE"` + "\n" + script + "\n"
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(full), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FFMPEG_ARGS_FILE", argsFile)
	return argsFile
}

func readFFmpegArgs(t *testing.T, argsFile string) string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMuxStreamsSurfacesFFmpegStderr(t *testing.T) {
	stubFFmpeg(t, `echo "Error opening input: invalid data found" >&2; exit 1`)

	err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false)
	if err == nil {
		t.Fatal("expected an error when ffmpeg fails")
	}
	if !strings.Contains(err.Error(), "invalid data found") {
		t.Fatalf("ffmpeg's stderr must be included in the error, got: %v", err)
	}
}

func TestMuxStreamsSucceedsWhenFFmpegDoes(t *testing.T) {
	stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMuxStreamsCopiesAudioWhenAlreadyAAC(t *testing.T) {
	argsFile := stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", true); err != nil {
		t.Fatal(err)
	}
	args := readFFmpegArgs(t, argsFile)
	if !strings.Contains(args, "-c:a copy") {
		t.Fatalf("AAC audio must be stream-copied (lossless, fast), got args: %s", args)
	}
}

func TestMuxStreamsTranscodesAudioWhenNotAAC(t *testing.T) {
	argsFile := stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), "video.webm", "audio.webm", "out.part.mp4", false); err != nil {
		t.Fatal(err)
	}
	args := readFFmpegArgs(t, argsFile)
	if !strings.Contains(args, "-c:a aac") {
		t.Fatalf("non-AAC audio must be transcoded to AAC for the mp4 container, got args: %s", args)
	}
}

func TestAudioCopyable(t *testing.T) {
	aac := &ytdl.Format{MimeType: `audio/mp4; codecs="mp4a.40.2"`}
	opus := &ytdl.Format{MimeType: `audio/webm; codecs="opus"`}
	if !audioCopyable(aac) {
		t.Error("audio/mp4 (AAC) should be copyable into the mp4 container")
	}
	if audioCopyable(opus) {
		t.Error("audio/webm (opus) must be transcoded, not copied")
	}
}

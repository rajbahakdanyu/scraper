// internal/parser/youtube/youtube_test.go
package youtube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ytdl "github.com/kkdai/youtube/v2"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestCopyStreamToFileRefusesSymlink(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "outside")
	if err := os.WriteFile(target, []byte("untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(dest, "video.part")
	if err := os.Symlink(target, partPath); err != nil {
		t.Fatal(err)
	}

	err := copyStreamToFile(context.Background(), io.LimitReader(strings.NewReader("fresh"), 5), 5, partPath, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to follow symlink") {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "untouched" {
		t.Fatalf("symlink target was modified: %q", got)
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
	_, err := scraper.Parse(context.Background(), "https://www.youtube.com/")
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

func TestSafeOutputPathDoesNotDuplicateMP4Extension(t *testing.T) {
	got := safeOutputPath("/tmp/downloads", "My Video.mp4")
	if filepath.Base(got) != "My Video.mp4" {
		t.Fatalf("safeOutputPath(...) base = %q, want %q", filepath.Base(got), "My Video.mp4")
	}
}

func TestSafeOutputPathAddsMP4ForDotContainingTitle(t *testing.T) {
	got := safeOutputPath("/tmp/downloads", "Mr. Brightside")
	if !strings.HasSuffix(got, ".mp4") {
		t.Fatalf("safeOutputPath(...) = %q, want an .mp4 suffix", got)
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

func TestSelectFormatsPicksHighestResolution(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 360, Bitrate: 500},
			{Height: 1080, Bitrate: 4000},
			{Height: 720, Bitrate: 2000},
			{Height: 0, Bitrate: 128, AudioChannels: 2}, // audio-only
			{Height: 0, Bitrate: 256, AudioChannels: 2}, // audio-only, higher bitrate
		},
	}
	videoFmt, audioFmt := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p video format, got %+v", videoFmt)
	}
	if audioFmt == nil || audioFmt.Bitrate != 256 {
		t.Fatalf("expected the 256-bitrate audio format, got %+v", audioFmt)
	}
}

func TestSelectFormatsPrefersDefaultAudioLanguage(t *testing.T) {
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

	_, audioFmt := selectFormats(video, 0)
	if audioFmt == nil || audioFmt.AudioTrack == nil || !audioFmt.AudioTrack.AudioIsDefault {
		t.Fatalf("selected non-default audio: %+v", audioFmt)
	}
}

func TestFormatsForDownloadRejectsMuteVideo(t *testing.T) {
	video := &ytdl.Video{ID: "mute", Formats: ytdl.FormatList{{Height: 1080, Bitrate: 4000}}}
	if _, _, err := formatsForDownload(video, 0, false); err == nil || !strings.Contains(err.Error(), "no audio format") {
		t.Fatalf("expected no-audio error, got %v", err)
	}
}

func TestSelectFormatsSkipsAudioWhenAlreadyMuxed(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000, AudioChannels: 2}, // already muxed
			{Height: 0, Bitrate: 256, AudioChannels: 2},
		},
	}
	videoFmt, audioFmt := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p video format, got %+v", videoFmt)
	}
	if audioFmt != nil {
		t.Errorf("expected no separate audio format for an already-muxed stream, got %+v", audioFmt)
	}
}

func TestSelectFormatsBreaksHeightTiesByBitrate(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 3000},
			{Height: 1080, Bitrate: 5000},
		},
	}
	videoFmt, _ := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Bitrate != 5000 {
		t.Fatalf("expected the higher-bitrate 1080p format, got %+v", videoFmt)
	}
}

func TestSelectFormatsNoFormats(t *testing.T) {
	videoFmt, audioFmt := selectFormats(&ytdl.Video{}, 0)
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
	if got := estimatedSize(video, 0); got != 1200 {
		t.Errorf("estimatedSize = %d, want 1200", got)
	}
}

func TestNewDownloaderEnforcesMinConcurrency(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{})
	if cap(d.sem) != 1 {
		t.Errorf("expected a concurrency floor of 1, got %d", cap(d.sem))
	}
}

func TestNewDownloaderWithConfigAppliesTransferPolicy(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{
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
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
	item := model.MediaItem{URL: "not-a-youtube-url"}

	if err := d.Download(item, t.TempDir()); err == nil {
		t.Fatal("expected an error for a URL with no extractable video ID")
	}
}

func TestDownloaderCancelIsSafeForUnknownItem(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
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

func TestWithRetryDoesNotRetryPermanentErrors(t *testing.T) {
	attempts := 0
	err := withRetry(t.Context(), func() error {
		attempts++
		return model.NewNonRetryable(errors.New("youtube: no video formats found for xyz"))
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if attempts != 1 {
		t.Fatalf("permanent error was retried: attempts = %d, want 1", attempts)
	}
}

func TestDownloadBestNoFormatsIsPermanent(t *testing.T) {
	// A video whose metadata carries no downloadable formats can never
	// succeed no matter how many times the download is retried, so
	// downloadBest must classify the failure as non-retryable.
	scraper := NewYouTubeScraper()
	scraper.parsed["nofmts"] = &ytdl.Video{ID: "nofmts", Title: "No Formats"}

	err := scraper.downloadBest(t.Context(), "nofmts", t.TempDir(), "owner", fileutil.NewPathAllocator(), nil, true, 0, "", nil)
	if err == nil {
		t.Fatal("expected an error for a video with no downloadable formats")
	}
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("format-selection failure must be classified permanent, got %T: %v", err, err)
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

func TestMuxStreamsMissingFFmpegIsPermanent(t *testing.T) {
	// No ffmpeg on PATH at all: the exec-startup failure must be
	// classified permanent — no amount of retrying installs ffmpeg.
	t.Setenv("PATH", t.TempDir())

	err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false)
	if err == nil {
		t.Fatal("expected an error when ffmpeg is not installed")
	}
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("a missing ffmpeg binary must be permanent (not retried), got %T: %v", err, err)
	}
}

func TestMuxStreamsMuxFailureStaysRetryable(t *testing.T) {
	// ffmpeg started fine but rejected the inputs (e.g. a truncated or
	// corrupt stream): a retry downloads fresh bytes and can succeed, so
	// a mux failure must stay retryable.
	stubFFmpeg(t, `exit 1`)

	err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false)
	if err == nil {
		t.Fatal("expected an error when ffmpeg fails")
	}
	var nre *model.NonRetryableError
	if errors.As(err, &nre) {
		t.Fatalf("a mux failure must stay retryable, got %T: %v", err, err)
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

func TestExtractAudioM4a(t *testing.T) {
	// When ffmpeg is available, verify m4a extraction copies audio codec
	// (lossless pass-through: -vn -acodec copy)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	stubFFmpeg(t, `
ext="$(echo "$*" | grep -o '[^ ]*\.m4a$' || true)"
if echo "$*" | grep -q '\-acodec copy'; then
  touch "$ext"
  echo "copied" >&2
fi
exit 0
`)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	if err := os.WriteFile(src, []byte("video+audio"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.m4a")
	if err := extractAudio(t.Context(), src, out, "m4a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("expected output file to exist")
	}
}

func TestExtractAudioMp3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	stubFFmpeg(t, `
ext="$(echo "$*" | grep -o '[^ ]*\.mp3$' || true)"
if echo "$*" | grep -q 'libmp3lame'; then
  touch "$ext"
  echo "transcoded" >&2
fi
exit 0
`)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.m4a")
	if err := os.WriteFile(src, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp3")
	if err := extractAudio(t.Context(), src, out, "mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("expected mp3 output file to exist")
	}
}

func TestExtractAudioMp3FromMuxedStream(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	stubFFmpeg(t, `
if echo "$*" | grep -q 'libmp3lame' && echo "$*" | grep -q '\-vn'; then
  ext="$(echo "$*" | grep -o '[^ ]*\.mp3$')"
  touch "$ext"
fi
exit 0
`)
	dir := t.TempDir()
	src := filepath.Join(dir, "muxed.mp4")
	if err := os.WriteFile(src, []byte("muxed"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp3")
	if err := extractAudio(t.Context(), src, out, "mp3"); err != nil {
		t.Fatal(err)
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

// --- Fake YouTube server ---
//
// The tests below run Parse and Download against a fake youtube.com so the
// number of video-info fetches can be counted. Every Client.GetVideoContext
// call shows up here as exactly one POST to /youtubei/v1/player (plus a
// watch-page GET for the publish date), so counting player posts counts
// metadata fetches.

type fakeYouTube struct {
	server      *httptest.Server
	playerPosts atomic.Int32
	// blockStream makes /stream hang until the client goes away, for
	// in-flight cancellation tests; streamRequests counts /stream hits.
	blockStream    bool
	streamRequests atomic.Int32
}

// startFakeYouTube serves every endpoint the ytdl client touches: the front
// page (visitor-ID bootstrap before any POST), the innertube player API, the
// watch page (embedding the same player response JSON), the embed page and
// base.js player config fetched while resolving the stream URL, and the
// media stream itself (honoring the client's ?range=start-end chunk
// parameter).
func startFakeYouTube(t *testing.T, videoID, title, content string) *fakeYouTube {
	t.Helper()
	f := &fakeYouTube{}

	var playerResponse, watchPage string
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			fmt.Fprint(w, "\nytcfg.set("+`{"INNERTUBE_CONTEXT":{"client":{"visitorData":"fakevisitor"}}}`+");")
		case r.Method == http.MethodPost && r.URL.Path == "/youtubei/v1/player":
			f.playerPosts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, playerResponse)
		case r.URL.Path == "/watch":
			fmt.Fprint(w, watchPage)
		case strings.HasPrefix(r.URL.Path, "/embed/"):
			fmt.Fprint(w, `<html>"jsUrl":"/s/player/fake1234/player_ias.vflset/en_US/base.js"</html>`)
		case strings.HasPrefix(r.URL.Path, "/s/player/"):
			fmt.Fprint(w, "// fake base.js; the fake stream URL has no n-parameter to decode")
		case r.URL.Path == "/stream":
			f.streamRequests.Add(1)
			if f.blockStream {
				<-r.Context().Done() // hang until the client aborts
				return
			}
			if rangeParam := r.URL.Query().Get("range"); rangeParam != "" {
				var start, end int
				if _, err := fmt.Sscanf(rangeParam, "%d-%d", &start, &end); err != nil || end >= len(content) {
					http.Error(w, "bad range", http.StatusBadRequest)
					return
				}
				fmt.Fprint(w, content[start:end+1])
				return
			}
			fmt.Fprint(w, content)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)

	// contentLength is a string in the real player response JSON.
	playerResponse = fmt.Sprintf(`{"playabilityStatus":{"status":"OK"},`+
		`"videoDetails":{"videoId":%q,"title":%q,"lengthSeconds":"5","author":"tester"},`+
		`"streamingData":{"formats":[{"itag":18,"url":%q,"mimeType":"video/mp4","bitrate":1000,`+
		`"width":640,"height":360,"contentLength":"%d","audioChannels":2}]}}`,
		videoID, title, f.server.URL+"/stream", len(content))
	watchPage = `<html><script>var ytInitialPlayerResponse = ` + playerResponse + `;</script></html>`
	return f
}

// wire routes all of scraper's HTTP through the fake. It must be called
// after NewDownloaderWithConfig, which installs its own streaming client on
// the scraper.
func (f *fakeYouTube) wire(scraper *Scraper) {
	f.playerPosts.Store(0)
	scraper.video.HTTPClient = &http.Client{Transport: fakeYouTubeTransport{host: f.server.Listener.Addr().String()}}
}

// fakeYouTubeTransport rewrites every request to hit the fake server,
// whatever host the ytdl client originally asked for.
type fakeYouTubeTransport struct{ host string }

func (t fakeYouTubeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	redirected := req.Clone(req.Context())
	redirected.URL.Scheme = "http"
	redirected.URL.Host = t.host
	return http.DefaultTransport.RoundTrip(redirected)
}

func assertDownloadedFile(t *testing.T, dir, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("downloaded %s = %q, want %q", name, got, want)
	}
}

func TestParseThenDownloadFetchesVideoInfoOnce(t *testing.T) {
	const videoID = "testvideo01"
	content := "fake video bytes for the single-fetch test"
	fake := startFakeYouTube(t, videoID, "Test Video", content)

	scraper := NewYouTubeScraper()
	d, _ := NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: 1})
	fake.wire(scraper)

	items, err := scraper.Parse(context.Background(), "https://www.youtube.com/watch?v="+videoID)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Parse returned %d items, want 1", len(items))
	}

	dest := t.TempDir()
	if err := d.Download(items[0], dest); err != nil {
		t.Fatalf("Download: %v", err)
	}

	if got := fake.playerPosts.Load(); got != 1 {
		t.Errorf("video info fetches across parse+download = %d, want exactly 1", got)
	}
	assertDownloadedFile(t, dest, "Test Video.mp4", content)
}

func TestDownloadWithoutParseFetchesVideoInfoOnce(t *testing.T) {
	// Playlist items reach Download without Parse ever resolving their video
	// info, so the download path must still fetch it by itself (exactly once).
	const videoID = "testvideo02"
	content := "fake video bytes for the download-only fetch test"
	fake := startFakeYouTube(t, videoID, "Test Video", content)

	scraper := NewYouTubeScraper()
	d, _ := NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: 1})
	fake.wire(scraper)

	dest := t.TempDir()
	item := model.MediaItem{URL: videoWatchURL(videoID), Title: "Test Video", Type: model.MediaTypeVideo}
	if err := d.Download(item, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}

	if got := fake.playerPosts.Load(); got != 1 {
		t.Errorf("video info fetches = %d, want exactly 1", got)
	}
	assertDownloadedFile(t, dest, "Test Video.mp4", content)
}

func TestDownloadNoFormatsFailsAfterOneAttempt(t *testing.T) {
	// A format-selection failure is permanent: Download must fail after
	// exactly one attempt instead of downloading nothing three times.
	const videoID = "noformats01"
	fake := startFakeYouTube(t, videoID, "No Formats", "fake content")

	scraper := NewYouTubeScraper()
	d, _ := NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: 1})
	fake.wire(scraper)
	// Seed a format-less video as if Parse had fetched it. Any retry
	// attempt after the first would re-fetch the metadata and show up
	// here as an extra player post.
	scraper.parsed[videoID] = &ytdl.Video{ID: videoID, Title: "No Formats"}

	item := model.MediaItem{URL: videoWatchURL(videoID), Title: "No Formats", Type: model.MediaTypeVideo}
	if err := d.Download(item, t.TempDir()); err == nil {
		t.Fatal("expected an error for a video with no downloadable formats")
	}
	if got := fake.playerPosts.Load(); got != 0 {
		t.Errorf("permanent format-selection failure was retried: %d extra metadata fetches, want 0", got)
	}
}

// waitProgressClosed drains ch until it closes, failing the test if that
// takes longer than timeout.
func waitProgressClosed(t *testing.T, ch <-chan model.ProgressEvent, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("progress channel was not closed")
		}
	}
}

func TestDownloaderShutdownCancelsInFlightDownload(t *testing.T) {
	const videoID = "shutdown01"
	fake := startFakeYouTube(t, videoID, "Slow Video", "some stream content")
	fake.blockStream = true

	scraper := NewYouTubeScraper()
	d, _ := NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: 1})
	fake.wire(scraper)

	item := model.MediaItem{URL: videoWatchURL(videoID), Title: "Slow Video", Type: model.MediaTypeVideo}
	downloadDone := make(chan error, 1)
	go func() { downloadDone <- d.Download(item, t.TempDir()) }()

	// Wait until the stream request is genuinely in flight.
	startDeadline := time.Now().Add(5 * time.Second)
	for fake.streamRequests.Load() == 0 {
		if time.Now().After(startDeadline) {
			t.Fatal("stream request never started")
		}
		time.Sleep(time.Millisecond)
	}

	start := time.Now()
	d.Shutdown()

	select {
	case err := <-downloadDone:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected a cancellation error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not cancel the in-flight download")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("in-flight download took %v to abort after Shutdown", elapsed)
	}

	// Once the in-flight download has returned, the progress channel must
	// close so a consumer ranging over Progress() terminates.
	waitProgressClosed(t, d.Progress(), 5*time.Second)
}

func TestSelectFormatsQualityCap(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
			{Height: 720, Bitrate: 2000},
			{Height: 480, Bitrate: 1000},
			{Height: 0, Bitrate: 128, AudioChannels: 2},
		},
	}
	videoFmt, _ := selectFormats(video, 720)
	if videoFmt == nil || videoFmt.Height != 720 {
		t.Fatalf("expected 720p with quality cap, got %+v", videoFmt)
	}
}

func TestSelectFormatsMaxHeightZeroIsUncapped(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
			{Height: 720, Bitrate: 2000},
		},
	}
	videoFmt, _ := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p (uncapped), got %+v", videoFmt)
	}
}

func TestSelectFormatsHeightTieBreakByBitrate(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 720, Bitrate: 2000},
			{Height: 720, Bitrate: 4000},
		},
	}
	videoFmt, _ := selectFormats(video, 720)
	if videoFmt == nil || videoFmt.Bitrate != 4000 {
		t.Fatalf("expected higher-bitrate 720p, got %+v", videoFmt)
	}
}

func TestSelectFormatsQualityFallback(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 480, Bitrate: 1000},
			{Height: 360, Bitrate: 500},
		},
	}
	videoFmt, _ := selectFormats(video, 1080)
	if videoFmt == nil || videoFmt.Height != 480 {
		t.Fatalf("expected fallback to 480p, got %+v", videoFmt)
	}
}

func TestSelectBestAudio(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
			{Height: 0, Bitrate: 128, AudioChannels: 2},
			{Height: 0, Bitrate: 256, AudioChannels: 2},
		},
	}
	audioFmt := selectBestAudio(video)
	if audioFmt == nil || audioFmt.Bitrate != 256 {
		t.Fatalf("expected 256-bitrate audio, got %+v", audioFmt)
	}
}

func TestSelectBestAudioPrefersDefaultTrack(t *testing.T) {
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
		{Height: 0, Bitrate: 128, AudioChannels: 2, AudioTrack: nonDefaultTrack},
		{Height: 0, Bitrate: 64, AudioChannels: 2, AudioTrack: defaultTrack},
	}}
	audioFmt := selectBestAudio(video)
	if audioFmt == nil || audioFmt.AudioTrack == nil || !audioFmt.AudioTrack.AudioIsDefault {
		t.Fatalf("expected default audio track, got %+v", audioFmt)
	}
}

func TestSelectBestAudioNoAudioFormats(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
		},
	}
	audioFmt := selectBestAudio(video)
	if audioFmt != nil {
		t.Fatalf("expected nil when no audio-only formats, got %+v", audioFmt)
	}
}

func TestFormatsForDownloadAudioOnly(t *testing.T) {
	video := &ytdl.Video{ID: "test", Formats: ytdl.FormatList{
		{Height: 1080, Bitrate: 4000},
		{Height: 0, Bitrate: 128, AudioChannels: 2},
	}}
	videoFmt, audioFmt, err := formatsForDownload(video, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if videoFmt != nil {
		t.Fatalf("expected nil videoFmt for audio-only, got %+v", videoFmt)
	}
	if audioFmt == nil || audioFmt.Bitrate != 128 {
		t.Fatalf("expected audio format for audio-only, got %+v", audioFmt)
	}
}

func TestFormatsForDownloadAudioOnlyNoAudio(t *testing.T) {
	video := &ytdl.Video{ID: "mute", Formats: ytdl.FormatList{
		{Height: 1080, Bitrate: 4000},
	}}
	vidFmt, audFmt, err := formatsForDownload(video, 0, true)
	if err != nil {
		t.Fatalf("expected fallback to muxed format for audio-only, got error: %v", err)
	}
	if vidFmt == nil {
		t.Fatal("expected vidFmt (muxed fallback) for audio-only")
	}
	if audFmt != nil {
		t.Fatalf("expected nil audFmt for muxed fallback, got %+v", audFmt)
	}
}

func TestDownloaderFailsFastAfterShutdown(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
	d.Shutdown()

	// The channel closes asynchronously; the fail-fast path must never
	// send on it (a send on a closed channel would panic).
	waitProgressClosed(t, d.Progress(), 5*time.Second)

	start := time.Now()
	item := model.MediaItem{URL: videoWatchURL("shutdown02"), Title: "Late", Type: model.MediaTypeVideo}
	err := d.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected Download to fail fast after Shutdown")
	}
	if !strings.Contains(err.Error(), "shut down") {
		t.Errorf("expected a shutdown error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Download blocked for %v after Shutdown", elapsed)
	}
}

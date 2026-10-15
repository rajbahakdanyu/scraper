package youtube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/downloadercore"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"golang.org/x/time/rate"
)

func TestCopyStreamToFileRateLimitedLargeReads(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1<<30), downloadercore.MinRateLimitBurst)
	payload := strings.Repeat("x", 1<<20)
	path := filepath.Join(t.TempDir(), "f")
	if _, err := copyStreamToFile(t.Context(), strings.NewReader(payload), int64(len(payload)), path, limiter, nil); err != nil {
		t.Fatalf("rate-limited copy with reads larger than the burst failed: %v", err)
	}
}

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

	_, err := copyStreamToFile(context.Background(), io.LimitReader(strings.NewReader("fresh"), 5), 5, partPath, nil, nil)
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

func TestCopyStreamToFileReturnsInlineChecksum(t *testing.T) {
	content := "inline checksum"
	path := filepath.Join(t.TempDir(), "stream.bin")

	digest, err := copyStreamToFile(context.Background(), strings.NewReader(content), int64(len(content)), path, nil, nil)
	if err != nil {
		t.Fatalf("copyStreamToFile failed: %v", err)
	}
	if err := fileutil.WriteChecksum(path); err != nil {
		t.Fatalf("WriteChecksum failed: %v", err)
	}
	sidecar, err := os.ReadFile(fileutil.ChecksumPath(path))
	if err != nil {
		t.Fatalf("failed to read checksum sidecar: %v", err)
	}
	want := sha256.Sum256([]byte(content))
	wantDigest := hex.EncodeToString(want[:])
	if digest != wantDigest {
		t.Fatalf("inline digest = %q, want %q", digest, wantDigest)
	}
	if !strings.HasPrefix(string(sidecar), digest+"  ") {
		t.Fatalf("sidecar = %q, want digest prefix %q", sidecar, digest+"  ")
	}
}

func TestSafeOutputPathIsAlwaysAbsolute(t *testing.T) {
	// A relative dest ("." is what a default --output resolves to) must
	// still yield an absolute path, even for a title starting with "-".
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

func TestCopyStreamToFileRejectsTruncatedStream(t *testing.T) {
	// The stream ends cleanly (io.EOF) after fewer bytes than the format
	// metadata promised — without a size check this truncated file would be
	// committed and its checksum sidecar would bless the truncation forever.
	short := strings.NewReader("only-partial-content")
	path := filepath.Join(t.TempDir(), "video.mp4")

	_, err := copyStreamToFile(t.Context(), short, 1<<20, path, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected a size-mismatch error for a truncated stream, got %v", err)
	}
}

func TestCopyStreamToFileAcceptsExactStream(t *testing.T) {
	content := strings.Repeat("x", 4096)
	path := filepath.Join(t.TempDir(), "video.mp4")

	if _, err := copyStreamToFile(t.Context(), strings.NewReader(content), int64(len(content)), path, nil, nil); err != nil {
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

	if _, err := copyStreamToFile(t.Context(), strings.NewReader("whatever"), 0, path, nil, nil); err != nil {
		t.Fatalf("size 0 (unknown) must not trigger the size check, got %v", err)
	}
}

func TestDownloadBestNoFormatsIsPermanent(t *testing.T) {
	// A video whose metadata carries no downloadable formats can never
	// succeed no matter how many times the download is retried, so
	// downloadBest must classify the failure as non-retryable.
	scraper := NewYouTubeScraper()
	scraper.parsed["nofmts"] = &ytdl.Video{ID: "nofmts", Title: "No Formats"}

	_, err := scraper.downloadBest(t.Context(), "nofmts", t.TempDir(), "owner", fileutil.NewPathAllocator(), nil, true, 0, "", nil)
	if err == nil {
		t.Fatal("expected an error for a video with no downloadable formats")
	}
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("format-selection failure must be classified permanent, got %T: %v", err, err)
	}
}

func TestDownloadBestWarnsOnQualityFallback(t *testing.T) {
	scraper := NewYouTubeScraper()
	var warnings []string
	scraper.warn = func(msg string) { warnings = append(warnings, msg) }
	scraper.parsed["v"] = &ytdl.Video{ID: "v", Title: "Clip", Formats: ytdl.FormatList{
		{Height: 360, AudioChannels: 2, MimeType: "video/mp4"},
	}}
	dest := t.TempDir()
	// An existing output makes downloadBest skip the network fetch.
	if err := os.WriteFile(filepath.Join(dest, "Clip.mp4"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := scraper.downloadBest(t.Context(), "v", dest, "owner", fileutil.NewPathAllocator(), nil, true, 240, "", nil); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "240p not available, using 360p") {
		t.Fatalf("expected one quality-fallback warning, got %q", warnings)
	}
}

func TestStreamPairErrReportsRootCause(t *testing.T) {
	boom := errors.New("boom")
	cancelled := fmt.Errorf("read: %w", context.Canceled)
	if err := streamPairErr(nil, nil); err != nil {
		t.Fatalf("no errors = %v", err)
	}
	if err := streamPairErr(cancelled, boom); !errors.Is(err, boom) || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("audio failure that cancelled video = %v, want the audio error", err)
	}
	if err := streamPairErr(boom, cancelled); !errors.Is(err, boom) || !strings.Contains(err.Error(), "video") {
		t.Fatalf("video failure that cancelled audio = %v, want the video error", err)
	}
}

func TestDownloadAndMuxDownloadsSeparateStreams(t *testing.T) {
	const videoID = "mux-video-01"
	videoContent := "video stream bytes"
	audioContent := "audio stream bytes"
	fake := startFakeYouTube(t, videoID, "Muxed title", "unused")
	fake.streamContents["/stream/video"] = videoContent
	fake.streamContents["/stream/audio"] = audioContent
	fake.playerResponses[videoID] = fmt.Sprintf(`{"playabilityStatus":{"status":"OK"},`+
		`"videoDetails":{"videoId":%q,"title":"Muxed title","lengthSeconds":"5","author":"tester"},`+
		`"streamingData":{"formats":[`+
		`{"itag":137,"url":%q,"mimeType":"video/mp4","bitrate":1000,"width":1920,"height":1080,"contentLength":"%d"},`+
		`{"itag":140,"url":%q,"mimeType":"audio/mp4; codecs=\"mp4a.40.2\"","bitrate":128,"audioChannels":2,"contentLength":"%d"}]}}`,
		videoID, fake.server.URL+"/stream/video", len(videoContent), fake.server.URL+"/stream/audio", len(audioContent))
	stubFFmpeg(t, `output=""; for arg do output="$arg"; done; printf 'predictable muxed output' > "$output"`)

	scraper := NewYouTubeScraper()
	d, err := NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	fake.wire(scraper)
	dest := t.TempDir()
	path, err := d.Download(model.MediaItem{URL: videoWatchURL(videoID), Title: "Muxed title", Type: model.MediaTypeVideo}, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("muxed output %q missing or empty: stat error=%v", path, err)
	}
	assertDownloadedFile(t, dest, "Muxed title.mp4", "predictable muxed output")
	present, matches, err := fileutil.VerifyChecksum(path)
	if err != nil || !present || !matches {
		t.Fatalf("muxed checksum verification = present %v matches %v err %v", present, matches, err)
	}
}

func TestDownloadAudioOnlyProducesM4A(t *testing.T) {
	const videoID = "audio-video-01"
	audioContent := "audio-only stream bytes"
	fake := startFakeYouTube(t, videoID, "Audio title", "unused")
	fake.streamContents["/stream/audio"] = audioContent
	fake.playerResponses[videoID] = fmt.Sprintf(`{"playabilityStatus":{"status":"OK"},`+
		`"videoDetails":{"videoId":%q,"title":"Audio title","lengthSeconds":"5","author":"tester"},`+
		`"streamingData":{"formats":[`+
		`{"itag":140,"url":%q,"mimeType":"audio/mp4; codecs=\"mp4a.40.2\"","bitrate":128,"audioChannels":2,"contentLength":"%d"}]}}`,
		videoID, fake.server.URL+"/stream/audio", len(audioContent))
	stubFFmpeg(t, `output=""; for arg do output="$arg"; done; printf 'predictable audio output' > "$output"`)

	scraper := NewYouTubeScraper()
	d, err := NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: 1, AudioOnly: "m4a"})
	if err != nil {
		t.Fatal(err)
	}
	fake.wire(scraper)
	dest := t.TempDir()
	path, err := d.Download(model.MediaItem{URL: videoWatchURL(videoID), Title: "Audio title", Type: model.MediaTypeVideo}, dest)
	if err != nil {
		t.Fatalf("audio-only Download: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("audio output %q missing or empty: stat error=%v", path, err)
	}
	if filepath.Ext(path) != ".m4a" {
		t.Fatalf("audio-only output extension = %q, want .m4a", filepath.Ext(path))
	}
	assertDownloadedFile(t, dest, "Audio title.m4a", "predictable audio output")
}

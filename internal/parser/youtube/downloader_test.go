package youtube

import (
	"context"
	"strings"
	"testing"
	"time"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestNewDownloaderWithConfigAppliesTransferPolicy(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{
		Concurrency:  3,
		Timeout:      5 * time.Second,
		UserAgent:    "Custom/1.0",
		RateLimit:    1024,
		SkipChecksum: true,
		Quality:      "720p",
	})
	if d.Limiter() == nil || !d.SkipSum() || d.quality != 720 || d.qualityLabel != "720p" {
		t.Fatalf("policy not applied: limiter=%v skip=%v quality=%d/%q", d.Limiter(), d.SkipSum(), d.quality, d.qualityLabel)
	}
}

func TestDownloaderReturnsErrorForUnresolvableURL(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
	item := model.MediaItem{URL: "not-a-youtube-url"}

	if _, err := d.Download(item, t.TempDir()); err == nil {
		t.Fatal("expected an error for a URL with no extractable video ID")
	}
}

func TestDownloaderCancelIsSafeForUnknownItem(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
	// Must not panic even though nothing is downloading for this item.
	d.Cancel(model.MediaItem{URL: "https://www.youtube.com/watch?v=xyz"})
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
	if _, err := d.Download(items[0], dest); err != nil {
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
	if _, err := d.Download(item, dest); err != nil {
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
	if _, err := d.Download(item, t.TempDir()); err == nil {
		t.Fatal("expected an error for a video with no downloadable formats")
	}
	if got := fake.playerPosts.Load(); got != 0 {
		t.Errorf("permanent format-selection failure was retried: %d extra metadata fetches, want 0", got)
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
	go func() { _, err := d.Download(item, t.TempDir()); downloadDone <- err }()

	// Wait until the stream request is genuinely in flight.
	select {
	case <-fake.streamStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("stream request never started")
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

func TestDownloaderFailsFastAfterShutdown(t *testing.T) {
	d, _ := NewDownloaderWithConfig(NewYouTubeScraper(), DownloaderConfig{Concurrency: 1})
	d.Shutdown()

	// The channel closes asynchronously; the fail-fast path must never
	// send on it (a send on a closed channel would panic).
	waitProgressClosed(t, d.Progress(), 5*time.Second)

	start := time.Now()
	item := model.MediaItem{URL: videoWatchURL("shutdown02"), Title: "Late", Type: model.MediaTypeVideo}
	_, err := d.Download(item, t.TempDir())
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

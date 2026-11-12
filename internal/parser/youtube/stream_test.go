package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

type instantSleeper struct{}

func (instantSleeper) Sleep(time.Duration)                                 {}
func (instantSleeper) SleepCtx(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func useTinyChunks(t *testing.T) {
	t.Helper()
	size, workers := streamChunkSize, streamChunkWorkers
	streamChunkSize, streamChunkWorkers = 4, 1
	t.Cleanup(func() { streamChunkSize, streamChunkWorkers = size, workers })
}

// rangeRecorder logs every stream Range header and, while failFrom > 0,
// answers ranges starting at or after that offset with a 500.
type rangeRecorder struct {
	mu       sync.Mutex
	ranges   []string
	failFrom int64
}

func (r *rangeRecorder) hook(w http.ResponseWriter, req *http.Request) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rng := req.Header.Get("Range")
	r.ranges = append(r.ranges, rng)
	var start int64
	if _, err := fmt.Sscanf(rng, "bytes=%d-", &start); err == nil && r.failFrom > 0 && start >= r.failFrom {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	return false
}

func (r *rangeRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	got := r.ranges
	r.ranges, r.failFrom = nil, 0
	return got
}

func workDirs(t *testing.T, dest string) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(dest, ".scraper-yt-*"))
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

func TestDownloadResumesInterruptedStreamOnNextRun(t *testing.T) {
	useTinyChunks(t)
	const videoID = "resume-vid1"
	content := "0123456789abcdef"
	fake := startFakeYouTube(t, videoID, "Resumed", content)
	rec := &rangeRecorder{failFrom: 8}
	fake.streamHook = rec.hook

	dest := t.TempDir()
	item := model.MediaItem{URL: videoWatchURL(videoID), Title: "Resumed", Type: model.MediaTypeVideo}
	newDownloader := func() *Downloader {
		scraper := NewYouTubeScraper()
		d, err := NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		d.sleeper = instantSleeper{}
		fake.wire(scraper)
		return d
	}

	if _, err := newDownloader().Download(item, dest); err == nil {
		t.Fatal("download with a failing server succeeded")
	}
	dirs := workDirs(t, dest)
	if len(dirs) != 1 {
		t.Fatalf("work dirs after a transient failure = %v, want one kept for resume", dirs)
	}
	if partial, err := os.ReadFile(filepath.Join(dirs[0], workSource)); err != nil || string(partial) != "01234567" {
		t.Fatalf("partial stream = %q, %v; want the first two chunks", partial, err)
	}
	rec.take()

	path, err := newDownloader().Download(item, dest)
	if err != nil {
		t.Fatalf("resumed Download: %v", err)
	}
	if got := rec.take(); !slices.Contains(got, "bytes=8-11") || slices.ContainsFunc(got, func(r string) bool {
		return r == "bytes=0-3" || r == "bytes=4-7"
	}) {
		t.Fatalf("resumed run requested ranges %v, want to continue from bytes=8-11", got)
	}
	assertDownloadedFile(t, dest, "Resumed.mp4", content)
	if present, matches, err := fileutil.VerifyChecksum(path); err != nil || !present || !matches {
		t.Fatalf("checksum of resumed file: present %v matches %v err %v", present, matches, err)
	}
	if dirs := workDirs(t, dest); len(dirs) != 0 {
		t.Fatalf("work dirs after success = %v, want none", dirs)
	}
}

func TestStreamRestartsWhenPartialBelongsToAnotherStream(t *testing.T) {
	useTinyChunks(t)
	content := "0123456789abcdef"
	fake := startFakeYouTube(t, "restart-vid", "Restart", content)
	rec := &rangeRecorder{}
	fake.streamHook = rec.hook
	scraper := NewYouTubeScraper()
	fake.wire(scraper)

	video := &ytdl.Video{ID: "restart-vid"}
	format := &ytdl.Format{ItagNo: 18, URL: fake.server.URL + "/stream", ContentLength: int64(len(content)), LastModified: "2"}
	path := filepath.Join(t.TempDir(), workSource)
	stale, _ := json.Marshal(streamResume{VideoID: video.ID, Itag: 18, ContentLength: format.ContentLength, LastModified: "1"})
	if err := os.WriteFile(path, []byte("XXXXXXXX"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+resumeSuffix, stale, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := scraper.downloadStreamToFileProgress(t.Context(), video, format, path, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); !slices.Contains(got, "bytes=0-3") {
		t.Fatalf("ranges = %v, want a restart from bytes=0-3", got)
	}
	if got, _ := os.ReadFile(path); string(got) != content {
		t.Fatalf("restarted stream = %q, want %q", got, content)
	}
	meta, _ := os.ReadFile(path + resumeSuffix)
	if !strings.Contains(string(meta), `"lastModified":"2"`) {
		t.Fatalf("resume metadata %s was not replaced", meta)
	}
}

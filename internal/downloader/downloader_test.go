// internal/downloader/downloader_test.go
package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

type noopSleeper struct{}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type cleanupTriggerBody struct {
	io.ReadCloser
	trigger func()
	once    sync.Once
}

func (b *cleanupTriggerBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(b.trigger)
	}
	return n, err
}

func (noopSleeper) Sleep(time.Duration)                           {}
func (noopSleeper) SleepCtx(context.Context, time.Duration) error { return nil }

type blockingSleeper struct {
	started chan struct{}
}

type cancelSleeper struct {
	started chan struct{}
	release chan struct{}
}

func (s cancelSleeper) Sleep(time.Duration) {}

func (s cancelSleeper) SleepCtx(ctx context.Context, _ time.Duration) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s blockingSleeper) Sleep(time.Duration) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {}
}

func (s blockingSleeper) SleepCtx(ctx context.Context, _ time.Duration) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Hour):
		return nil
	}
}

func newTestDownloader(concurrency int, timeout time.Duration, userAgent string) *Downloader {
	dl, _ := NewDownloaderWithConfig(concurrency, timeout, userAgent, "", nil)
	dl.client = &http.Client{}
	dl.SetSleeper(noopSleeper{})
	return dl
}

func TestDownload(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dl := newTestDownloader(5, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "test_video"}

	dest := t.TempDir()
	_, err := dl.Download(item, dest)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	finalPath := filepath.Join(dest, "test_video")
	if _, err := os.Stat(finalPath); os.IsNotExist(err) {
		entries, _ := os.ReadDir(dest)
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected final file %s to exist, found: %v", finalPath, names)
	}

	tempPath := finalPath + ".part"
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Error("expected .part temp file to be cleaned up")
	}
}

func TestDownloadRefusesSymlinkedPartFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5")
		_, _ = w.Write([]byte("fresh"))
	}))
	defer ts.Close()

	dest := t.TempDir()
	target := filepath.Join(dest, "outside")
	if err := os.WriteFile(target, []byte("untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dest, "video.part")); err != nil {
		t.Fatal(err)
	}

	dl := newTestDownloader(1, 0, "")
	_, err := dl.Download(model.MediaItem{URL: ts.URL, Title: "video"}, dest)
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

func TestDownloadUnknownLengthExceedsCapDiscardsPartial(t *testing.T) {
	oldMax := MaxUnknownFileSize
	MaxUnknownFileSize = 1024
	defer func() { MaxUnknownFileSize = oldMax }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2048))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dest := t.TempDir()
	item := model.MediaItem{URL: ts.URL, Title: "too-large.bin"}
	_, err := dl.Download(item, dest)
	if err == nil {
		t.Fatal("expected unknown-length download to exceed the cap")
	}
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("expected non-retryable cap error, got %T: %v", err, err)
	}
	for _, path := range []string{
		filepath.Join(dest, "too-large.bin"),
		filepath.Join(dest, "too-large.bin.part"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("expected capped download artifact %s to be removed, stat error: %v", path, statErr)
		}
	}
}

func TestDownloadUnknownLengthExactCapSucceeds(t *testing.T) {
	oldMax := MaxUnknownFileSize
	MaxUnknownFileSize = 1024
	defer func() { MaxUnknownFileSize = oldMax }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1024)) // exactly the cap
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dest := t.TempDir()
	item := model.MediaItem{URL: ts.URL, Title: "exact-cap.bin"}
	got, err := dl.Download(item, dest)
	if err != nil {
		t.Fatalf("a download of exactly MaxUnknownFileSize bytes must succeed, got: %v", err)
	}
	if info, statErr := os.Stat(got); statErr != nil || info.Size() != 1024 {
		t.Fatalf("downloaded file = %s, want 1024 bytes on disk", got)
	}
}

func TestDownloadRetry(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "retry_test"}

	_, err := dl.Download(item, t.TempDir())
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestDownloadCancelDuringRetryBackoff(t *testing.T) {
	requestStarted := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	sleeper := blockingSleeper{started: make(chan struct{})}
	dl.SetSleeper(sleeper)
	item := model.MediaItem{URL: ts.URL, Title: "cancel_backoff"}
	done := make(chan error, 1)
	go func() { _, err := dl.Download(item, t.TempDir()); done <- err }()

	<-requestStarted
	<-sleeper.started
	dl.Cancel(item)

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected cancellation error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt retry backoff")
	}
}

func TestDownloadAllFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "fail_test"}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected error for failing download")
	}
}

func TestCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	sleeper := cancelSleeper{started: make(chan struct{}), release: make(chan struct{})}
	dl.SetSleeper(sleeper)
	item := model.MediaItem{URL: ts.URL, Title: "cancel_test"}

	done := make(chan error, 1)
	go func() { _, err := dl.Download(item, t.TempDir()); done <- err }()
	<-sleeper.started
	dl.Cancel(item)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for cancelled download")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt the retry backoff")
	}
}

func TestCancelQueuedDownload(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Path == "/first" {
			close(firstStarted)
			<-releaseFirst
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	first := model.MediaItem{ID: "first", URL: ts.URL + "/first", Title: "first.bin"}
	second := model.MediaItem{ID: "second", URL: ts.URL + "/second", Title: "second.bin"}
	dest := t.TempDir()

	firstDone := make(chan error, 1)
	go func() { _, err := dl.Download(first, dest); firstDone <- err }()
	<-firstStarted

	dl.Prepare(second)
	secondDone := make(chan error, 1)
	go func() { _, err := dl.Download(second, dest); secondDone <- err }()

	deadline := time.Now().Add(time.Second)
	for {
		dl.mu.Lock()
		_, waiting := dl.cancelMap[second.Key()]
		dl.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second download never entered the queue")
		}
		time.Sleep(time.Millisecond)
	}

	dl.Cancel(second)
	select {
	case err := <-secondDone:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("queued download returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancellation did not return promptly")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("queued download reached server; requests=%d", got)
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first download failed: %v", err)
	}
}

func TestDownloadOnceTimesOutWaitingForResponseHeaders(t *testing.T) {
	started := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer ts.Close()

	dl, err := NewDownloaderWithConfig(1, 50*time.Millisecond, "", "", nil)
	if err != nil {
		t.Fatalf("NewDownloaderWithConfig failed: %v", err)
	}
	client, err := netutil.NewPublicStreamingClient(50*time.Millisecond, ts.URL)
	if err != nil {
		t.Fatalf("NewPublicStreamingClient failed: %v", err)
	}
	dl.client = client
	item := model.MediaItem{URL: ts.URL, Title: "headers.bin"}

	start := time.Now()
	_, err = dl.downloadOnce(context.Background(), item, item.URL, filepath.Join(t.TempDir(), "headers.bin.part"))
	<-started
	if err == nil {
		t.Fatal("expected response-header timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("response-header timeout took %v", elapsed)
	}
}

func TestNewDownloaderWithConfig(t *testing.T) {
	dl := newTestDownloader(3, 10*time.Second, "CustomAgent/2.0")
	if dl.userAgent != "CustomAgent/2.0" {
		t.Errorf("expected CustomAgent/2.0, got %s", dl.userAgent)
	}
	if cap(dl.sem) != 3 {
		t.Errorf("expected concurrency 3, got %d", cap(dl.sem))
	}
}

func TestNewDownloaderWithConfigAppliesCookies(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "session", Value: "test123"},
	}
	dl, err := NewDownloaderWithConfig(1, 30*time.Second, "", "", cookies)
	if err != nil {
		t.Fatal(err)
	}
	if dl.client.Jar == nil {
		t.Fatal("expected client.Jar to be set")
	}
}

func TestNewRequestError(t *testing.T) {
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: "://bad-url", Title: "bad"}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
	if !strings.Contains(err.Error(), "download failed") {
		t.Errorf("expected download failed error, got: %v", err)
	}
}

func TestDownloadResumesFromPartialFile(t *testing.T) {
	const full = "0123456789ABCDEFGHIJ"
	var mu sync.Mutex
	var sawRange string
	var sawIfRange string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawRange = r.Header.Get("Range")
		sawIfRange = r.Header.Get("If-Range")
		mu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Range", "bytes 10-19/20")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(full[10:]))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "resume.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "resume.bin.part")
	if err := os.WriteFile(tempPath, []byte(full[:10]), 0644); err != nil {
		t.Fatalf("failed to seed partial file: %v", err)
	}
	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: ts.URL, ETag: `"v1"`}); err != nil {
		t.Fatalf("failed to seed resume metadata: %v", err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("expected resumed download to succeed, got: %v", err)
	}

	mu.Lock()
	gotRange := sawRange
	mu.Unlock()
	if gotRange != "bytes=10-" {
		t.Errorf("Range header = %q, want %q", gotRange, "bytes=10-")
	}
	if sawIfRange != `"v1"` {
		t.Errorf("If-Range header = %q, want %q", sawIfRange, `"v1"`)
	}

	got, err := os.ReadFile(filepath.Join(dest, "resume.bin"))
	if err != nil {
		t.Fatalf("failed to read final file: %v", err)
	}
	if string(got) != full {
		t.Errorf("final content = %q, want %q", got, full)
	}
}

func TestDownloadFallsBackWhenServerIgnoresRange(t *testing.T) {
	const fresh = "HELLOWORLD"
	var sawRange string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRange = r.Header.Get("Range")
		// Ignores any Range header and always returns the full content,
		// simulating a server that doesn't support range requests.
		w.Header().Set("ETag", `"new"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fresh))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "fallback.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "fallback.bin.part")
	if err := os.WriteFile(tempPath, []byte("STALEGARBAGE"), 0644); err != nil {
		t.Fatalf("failed to seed partial file: %v", err)
	}
	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: ts.URL, ETag: `"old"`}); err != nil {
		t.Fatalf("failed to seed resume metadata: %v", err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("expected download to succeed, got: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "fallback.bin"))
	if err != nil {
		t.Fatalf("failed to read final file: %v", err)
	}
	if string(got) != fresh {
		t.Errorf("final content = %q, want %q (stale partial bytes must be discarded, not prepended)", got, fresh)
	}
	if sawRange != "bytes=12-" {
		t.Errorf("Range header = %q, want %q", sawRange, "bytes=12-")
	}
}

func TestDownloadRestartsAfter416(t *testing.T) {
	const fresh = "FreshFullContentHere"
	var requests int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fresh))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "restart416.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "restart416.bin.part")
	if err := os.WriteFile(tempPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to seed partial file: %v", err)
	}
	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: ts.URL, ETag: `"v1"`}); err != nil {
		t.Fatalf("failed to seed resume metadata: %v", err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("expected download to succeed after discarding a stale partial, got: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("expected exactly 2 requests (416 then a fresh retry), got %d", got)
	}

	gotContent, err := os.ReadFile(filepath.Join(dest, "restart416.bin"))
	if err != nil {
		t.Fatalf("failed to read final file: %v", err)
	}
	if string(gotContent) != fresh {
		t.Errorf("final content = %q, want %q", gotContent, fresh)
	}
}

func TestDownloadTreats416WithCompletePartialAsFinished(t *testing.T) {
	// A previous run downloaded the full body but died before the rename.
	// The resume request asks for bytes=<size>-, the server replies 416
	// with Content-Range "bytes */<size>" — the partial already IS the
	// complete resource and must be kept, not discarded and re-downloaded.
	const content = "AlreadyFullyDownloadedContent"
	var requests int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(content)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("re-downloaded-from-scratch"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "complete416.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "complete416.bin.part")
	if err := os.WriteFile(tempPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: ts.URL, ETag: `"v1"`}); err != nil {
		t.Fatal(err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("complete partial behind a 416 must succeed, got: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected exactly 1 request (no wasteful re-download), got %d", got)
	}
	got, err := os.ReadFile(filepath.Join(dest, "complete416.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("final content = %q, want the kept partial %q", got, content)
	}
}

func TestDownloadRejectsMismatchedContentRange(t *testing.T) {
	const fresh = "0123456789ABCDEFGHIJ"
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := atomic.AddInt32(&requests, 1)
		w.Header().Set("ETag", `"v1"`)
		if request == 1 {
			w.Header().Set("Content-Range", "bytes 0-9/20")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte(fresh[:10]))
			return
		}
		_, _ = w.Write([]byte(fresh))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "mismatch.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "mismatch.bin.part")
	if err := os.WriteFile(tempPath, []byte(fresh[:10]), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: ts.URL, ETag: `"v1"`}); err != nil {
		t.Fatal(err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("download failed after safe restart: %v", err)
	}
	if atomic.LoadInt32(&requests) != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	got, err := os.ReadFile(filepath.Join(dest, "mismatch.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fresh {
		t.Fatalf("final content = %q, want %q", got, fresh)
	}
}

func TestDownloadKeepsPartialFileAfterExhaustedRetries(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// Declare more bytes than actually sent, so the client sees an
			// unexpected EOF partway through — a genuinely partial,
			// resumable download rather than a clean error response.
			w.Header().Set("Content-Length", "20")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("PARTIAL8"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "partial_keep.bin"}
	dest := t.TempDir()

	if _, err := dl.Download(item, dest); err == nil {
		t.Fatal("expected download to fail after exhausting retries")
	}

	tempPath := filepath.Join(dest, "partial_keep.bin.part")
	info, statErr := os.Stat(tempPath)
	if statErr != nil {
		t.Fatalf("expected .part file to survive exhausted retries for a later resume, got: %v", statErr)
	}
	if info.Size() != 8 {
		t.Errorf("expected partial file to keep the 8 bytes written before the connection dropped, got %d bytes", info.Size())
	}
}

func TestDownloadRemovesPartialFileOnNonRetryableFailure(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.Header().Set("Content-Length", "20")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("PARTIAL8"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "partial_remove.bin"}
	dest := t.TempDir()

	if _, err := dl.Download(item, dest); err == nil {
		t.Fatal("expected download to fail")
	}

	tempPath := filepath.Join(dest, "partial_remove.bin.part")
	if _, statErr := os.Stat(tempPath); !os.IsNotExist(statErr) {
		t.Error("expected .part file to be removed after a non-retryable (404) failure")
	}
}

// TestDownloadResolvesURLAtDownloadTime pins the core lazy-resolution
// contract: an item with a Resolve func never has Resolve called before
// its download starts, and the HTTP request goes to the RESOLVED URL, not
// item.URL (which is only a stable placeholder).
func TestDownloadResolvesURLAtDownloadTime(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/placeholder" {
			// Trap: the placeholder URL must never be fetched.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte("resolved content"))
	}))
	defer ts.Close()

	var resolveCalls int32
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/placeholder",
		Title: "lazy.bin",
		Resolve: func(ctx context.Context) (string, error) {
			atomic.AddInt32(&resolveCalls, 1)
			return ts.URL + "/signed", nil
		},
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 0 {
		t.Fatalf("resolver called before Download started: %d calls", got)
	}

	dest := t.TempDir()
	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Errorf("expected exactly 1 resolve call, got %d", got)
	}
	got, err := os.ReadFile(filepath.Join(dest, "lazy.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "resolved content" {
		t.Errorf("final content = %q, want the resolved-URL content %q", got, "resolved content")
	}
}

// TestDownloadReResolvesOn403 simulates a signed URL expiring mid-queue:
// the first resolved URL 403s, the downloader must re-resolve once for a
// fresh signature and retry with it instead of failing permanently.
func TestDownloadReResolvesOn403(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Query().Get("sig") == "expired" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte("fresh content"))
	}))
	defer ts.Close()

	var resolveCalls int32
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/file",
		Title: "rotate.bin",
		Resolve: func(ctx context.Context) (string, error) {
			if atomic.AddInt32(&resolveCalls, 1) == 1 {
				return ts.URL + "/file?sig=expired", nil
			}
			return ts.URL + "/file?sig=fresh", nil
		},
	}

	dest := t.TempDir()
	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("expected re-resolve on 403 to recover the download, got: %v", err)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 2 {
		t.Errorf("expected 2 resolve calls (initial + re-resolve after 403), got %d", got)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("expected 2 HTTP requests (403 then success), got %d", got)
	}
	got, err := os.ReadFile(filepath.Join(dest, "rotate.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fresh content" {
		t.Errorf("final content = %q, want %q", got, "fresh content")
	}
}

// TestDownloadSecond403AfterReResolveIsPermanent caps the 403 override: a
// second 403 on the freshly re-resolved URL is genuinely permanent, not an
// invitation to keep re-resolving forever.
func TestDownloadSecond403AfterReResolveIsPermanent(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	var resolveCalls int32
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/file",
		Title: "forbidden.bin",
		Resolve: func(ctx context.Context) (string, error) {
			atomic.AddInt32(&resolveCalls, 1)
			return ts.URL + "/file", nil
		},
	}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected a permanent failure after a second 403")
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 2 {
		t.Errorf("expected exactly 2 resolve calls (re-resolved once), got %d", got)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("expected exactly 2 HTTP requests (initial 403 + one retry), got %d", got)
	}
}

// TestDownload403WithoutResolverNotRetried keeps today's behavior for
// items with no Resolve func: a 403 is nonRetryable, full stop.
func TestDownload403WithoutResolverNotRetried(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL + "/file", Title: "plain403.bin"}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected error for 403 download")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected exactly 1 request (nonRetryable 403, no resolver), got %d", got)
	}
}

func TestRedactSignedURLFromDownloadError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/placeholder",
		Title: "signed.bin",
		Resolve: func(ctx context.Context) (string, error) {
			return ts.URL + "/signed?token=secret", nil
		},
	}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected forbidden download to fail")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("download error exposed signed URL token: %v", err)
	}
	if !strings.Contains(err.Error(), ts.URL+"/signed") {
		t.Errorf("download error omitted redacted URL host/path: %v", err)
	}
}

// TestDownloadRetriesTransientResolverError: a resolver that fails
// transiently (network blip) is retried with backoff like any other
// transient download failure.
func TestDownloadRetriesTransientResolverError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	var resolveCalls int32
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/page",
		Title: "flaky.bin",
		Resolve: func(ctx context.Context) (string, error) {
			if atomic.AddInt32(&resolveCalls, 1) == 1 {
				return "", fmt.Errorf("connection reset by peer")
			}
			return ts.URL + "/file", nil
		},
	}

	dest := t.TempDir()
	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("expected transient resolver error to be retried successfully, got: %v", err)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 2 {
		t.Errorf("expected 2 resolve calls (error then success), got %d", got)
	}
}

// TestDownloadPermanentResolverErrorFailsImmediately: a resolver that
// reports the file as permanently gone (e.g. a 404 from the resolve API,
// wrapped in model.PermanentError) fails without retries and without ever
// hitting the download server.
func TestDownloadPermanentResolverErrorFailsImmediately(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()

	var resolveCalls int32
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/page",
		Title: "gone.bin",
		Resolve: func(ctx context.Context) (string, error) {
			atomic.AddInt32(&resolveCalls, 1)
			return "", &model.PermanentError{Err: fmt.Errorf("file deleted upstream")}
		},
	}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected permanent resolver error to fail the download")
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Errorf("permanent resolver error must not be retried, got %d resolve calls", got)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Errorf("no download request should be made when resolution fails permanently, got %d", got)
	}
}

// TestDownloadResumeMetadataOmitsResolvedURL: resolver items do not persist
// their signed URL in the resume sidecar.
func TestDownloadResumeMetadataOmitsResolvedURL(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Content-Length", "20")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("PARTIAL8"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	resolvedURL := ts.URL + "/signed?sig=abc"
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/page",
		Title: "meta.bin",
		Resolve: func(ctx context.Context) (string, error) {
			return resolvedURL, nil
		},
	}

	dest := t.TempDir()
	if _, err := dl.Download(item, dest); err == nil {
		t.Fatal("expected download to fail after exhausting retries")
	}

	metadata, err := readResumeMetadata(filepath.Join(dest, "meta.bin.part"))
	if err != nil {
		t.Fatalf("expected resume metadata to survive exhausted retries: %v", err)
	}
	if metadata.URL != "" {
		t.Errorf("resume metadata URL = %q, want it omitted for resolver items", metadata.URL)
	}
}

func TestMetadataFromResponseStoreURL(t *testing.T) {
	resp := &http.Response{Header: http.Header{"ETag": []string{`"v1"`}}}

	withURL := metadataFromResponse("https://example.com/file", resp, true)
	if withURL.URL != "https://example.com/file" {
		t.Errorf("metadata URL = %q, want raw URL", withURL.URL)
	}
	withoutURL := metadataFromResponse("https://example.com/file", resp, false)
	if withoutURL.URL != "" {
		t.Errorf("metadata URL = %q, want empty URL", withoutURL.URL)
	}
}

func TestWriteResumeMetadataUsesOwnerOnlyPermissions(t *testing.T) {
	tempPath := filepath.Join(t.TempDir(), "file.part")
	if err := writeResumeMetadata(tempPath, resumeMetadata{ETag: `"v1"`}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(resumeMetadataPath(tempPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("resume metadata permissions = %04o, want 0600", got)
	}
}

func TestWriteResumeMetadataRefusesSymlinkedTarget(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim.txt")
	if err := os.WriteFile(victim, []byte("do not clobber"), 0644); err != nil {
		t.Fatal(err)
	}
	tempPath := filepath.Join(root, "file.mp4.part")
	link := resumeMetadataPath(tempPath)
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}

	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: "https://example.com"}); err != nil {
		t.Fatalf("writeResumeMetadata: %v", err)
	}

	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "do not clobber" {
		t.Fatalf("symlinked victim file was clobbered: %q", data)
	}
}

// TestDownloadResumesAfterSignedURLRotation is the cross-run-resume win: an
// interrupted run leaves a .part plus resume metadata tied to an OLD
// signed URL; the next run resolves a FRESH signed URL for the SAME
// resource. The URL strings differ, but the stable ETag validator — not
// URL equality — is what protects resume integrity, so the partial must
// resume via a Range request instead of being discarded.
func TestDownloadResumesAfterSignedURLRotation(t *testing.T) {
	const full = "0123456789ABCDEFGHIJ"
	var mu sync.Mutex
	var sawRange, sawIfRange string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawRange = r.Header.Get("Range")
		sawIfRange = r.Header.Get("If-Range")
		mu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") == "" {
			// Trap: a from-scratch re-download means the partial was
			// wrongly discarded on the URL mismatch.
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("re-downloaded-from-scratch"))
			return
		}
		w.Header().Set("Content-Range", "bytes 10-19/20")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(full[10:]))
	}))
	defer ts.Close()

	var resolveCalls int32
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{
		URL:   ts.URL + "/page",
		Title: "rotated.bin",
		Resolve: func(ctx context.Context) (string, error) {
			atomic.AddInt32(&resolveCalls, 1)
			return ts.URL + "/file?sig=fresh", nil
		},
	}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "rotated.bin.part")
	if err := os.WriteFile(tempPath, []byte(full[:10]), 0644); err != nil {
		t.Fatal(err)
	}
	// Resume metadata from the previous run: OLD signed URL, stable ETag.
	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: "https://cdn.example.com/file?sig=expired", ETag: `"v1"`}); err != nil {
		t.Fatal(err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("expected resume across signed-URL rotation to succeed, got: %v", err)
	}
	mu.Lock()
	gotRange, gotIfRange := sawRange, sawIfRange
	mu.Unlock()
	if gotRange != "bytes=10-" {
		t.Errorf("Range header = %q, want %q (stale partial discarded on URL mismatch?)", gotRange, "bytes=10-")
	}
	if gotIfRange != `"v1"` {
		t.Errorf("If-Range header = %q, want %q", gotIfRange, `"v1"`)
	}
	got, err := os.ReadFile(filepath.Join(dest, "rotated.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != full {
		t.Errorf("final content = %q, want %q", got, full)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Errorf("expected 1 resolve call, got %d", got)
	}
}

func TestSetRateLimitThrottlesThroughput(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 96*1024) // well over minRateLimitBurst
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dl.SetRateLimit(20 * 1024) // 20KB/s
	item := model.MediaItem{URL: ts.URL, Title: "throttled.bin"}

	if _, err := dl.Download(item, t.TempDir()); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	var transfers int
	for {
		select {
		case evt := <-dl.Progress():
			if evt.Status == model.StatusDownloading {
				transfers++
			}
		default:
			if transfers < 2 {
				t.Fatalf("expected the limiter to split the transfer into waits, got %d progress events", transfers)
			}
			return
		}
	}
}

func TestSetRateLimitZeroRemovesCap(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 96*1024)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dl.SetRateLimit(20 * 1024)
	dl.SetRateLimit(0) // remove the cap before downloading
	item := model.MediaItem{URL: ts.URL, Title: "unthrottled.bin"}

	start := time.Now()
	if _, err := dl.Download(item, t.TempDir()); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("expected an uncapped download to be fast, took %v", elapsed)
	}
}

func TestDownloadWritesChecksumSidecar(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("checksum me"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "sum.bin"}
	dest := t.TempDir()

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("download failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dest, "sum.bin.sha256"))
	if err != nil {
		t.Fatalf("expected a .sha256 sidecar to be written, got: %v", err)
	}
	want := sha256.Sum256([]byte("checksum me"))
	wantHex := hex.EncodeToString(want[:])
	if !strings.HasPrefix(string(data), wantHex) {
		t.Errorf("sidecar content = %q, want it to start with %q", data, wantHex)
	}
}

func TestDownloadInlineChecksumMatchesWriteChecksum(t *testing.T) {
	const content = "inline downloader checksum"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		_, _ = w.Write([]byte(content))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "inline.bin.part")
	finalPath := filepath.Join(dest, "inline.bin")
	digest, err := dl.downloadOnce(context.Background(), model.MediaItem{URL: ts.URL}, ts.URL, tempPath)
	if err != nil {
		t.Fatalf("downloadOnce failed: %v", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if err := fileutil.WriteChecksum(finalPath); err != nil {
		t.Fatalf("WriteChecksum failed: %v", err)
	}
	sidecar, err := os.ReadFile(fileutil.ChecksumPath(finalPath))
	if err != nil {
		t.Fatalf("failed to read checksum sidecar: %v", err)
	}
	if !strings.HasPrefix(string(sidecar), digest+"  ") {
		t.Fatalf("inline digest = %q, sidecar = %q", digest, sidecar)
	}
}

func TestDownloadCompletesWhenResumeMetadataCleanupFails(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("ETag", `"cleanup-v1"`)
		w.Write([]byte("cleanup metadata"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	transport := http.DefaultTransport
	item := model.MediaItem{URL: ts.URL, Title: "cleanup.bin"}
	dest := t.TempDir()
	resumePath := filepath.Join(dest, "cleanup.bin.part.resume.json")

	dl.client = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := transport.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		resp.Body = &cleanupTriggerBody{
			ReadCloser: resp.Body,
			trigger: func() {
				if err := os.Remove(resumePath); err != nil {
					t.Fatalf("failed to replace resume metadata: %v", err)
				}
				if err := os.Mkdir(resumePath, 0755); err != nil {
					t.Fatalf("failed to create resume metadata directory: %v", err)
				}
				if err := os.WriteFile(filepath.Join(resumePath, "in-use"), []byte("keep"), 0644); err != nil {
					t.Fatalf("failed to block resume metadata cleanup: %v", err)
				}
			},
		}
		return resp, nil
	})}

	finalPath, err := dl.Download(item, dest)
	if err != nil {
		t.Fatalf("download failed when resume metadata cleanup failed: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("expected one download request, got %d", got)
	}
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("expected completed file %s: %v", finalPath, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "cleanup.bin.sha256")); err != nil {
		t.Fatalf("expected checksum sidecar: %v", err)
	}
}

func TestDownloadSkipsChecksumSidecarWhenDisabled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("no checksum"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dl.SetSkipChecksums(true)
	item := model.MediaItem{URL: ts.URL, Title: "nosum.bin"}
	dest := t.TempDir()

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "nosum.bin.sha256")); !os.IsNotExist(err) {
		t.Error("expected no .sha256 sidecar when checksums are disabled")
	}
}

func TestDownloadRedownloadsWhenChecksumMismatches(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Write([]byte("correct content"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "verify.bin"}
	dest := t.TempDir()

	finalPath := filepath.Join(dest, "verify.bin")
	if err := os.WriteFile(finalPath, []byte("CORRUPTED"), 0644); err != nil {
		t.Fatalf("failed to seed a corrupted final file: %v", err)
	}
	sum := sha256.Sum256([]byte("correct content"))
	sidecar := filepath.Join(dest, "verify.bin.sha256")
	if err := os.WriteFile(sidecar, []byte(hex.EncodeToString(sum[:])+"  verify.bin\n"), 0644); err != nil {
		t.Fatalf("failed to seed a checksum sidecar: %v", err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected the corrupted file to be redownloaded, got %d requests", got)
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("failed to read final file: %v", err)
	}
	if string(got) != "correct content" {
		t.Errorf("final content = %q, want %q", got, "correct content")
	}
}

func TestDownloadSkipsWhenChecksumMatches(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Write([]byte("should not be fetched"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "cached.bin"}
	dest := t.TempDir()

	finalPath := filepath.Join(dest, "cached.bin")
	content := []byte("already good")
	if err := os.WriteFile(finalPath, content, 0644); err != nil {
		t.Fatalf("failed to seed an existing file: %v", err)
	}
	sum := sha256.Sum256(content)
	sidecar := filepath.Join(dest, "cached.bin.sha256")
	if err := os.WriteFile(sidecar, []byte(hex.EncodeToString(sum[:])+"  cached.bin\n"), 0644); err != nil {
		t.Fatalf("failed to seed a checksum sidecar: %v", err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Errorf("expected no request for a file matching its checksum, got %d", got)
	}
}

func TestDownloadTreatsMalformedChecksumSidecarAsAbsent(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		_, _ = w.Write([]byte("new content"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "malformed.bin"}
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "malformed.bin"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "malformed.bin.sha256"), []byte("not-a-digest\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("a malformed sidecar must not fail the item: %v", err)
	}
	if requests != 0 {
		t.Fatalf("malformed sidecar unexpectedly triggered %d requests", requests)
	}
	// The existing file is trusted exactly as if no sidecar were present.
	got, err := os.ReadFile(filepath.Join(dest, "malformed.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("file content = %q, want existing", got)
	}
}

func TestDownloadSurfacesChecksumWriteFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "blocked.bin"}
	dest := t.TempDir()
	if err := os.Mkdir(filepath.Join(dest, "blocked.bin.sha256"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := dl.Download(item, dest)
	if err == nil || !strings.Contains(err.Error(), "write checksum") {
		t.Fatalf("expected checksum write error, got %v", err)
	}
}

func TestConcurrentDownloadsWithSameTitleUseDistinctPaths(t *testing.T) {
	server := func(content string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(content))
		}))
	}
	firstServer := server("first")
	defer firstServer.Close()
	secondServer := server("second")
	defer secondServer.Close()

	dl := newTestDownloader(2, 0, "")
	dest := t.TempDir()
	items := []model.MediaItem{
		{ID: "first", URL: firstServer.URL, Title: "same.bin"},
		{ID: "second", URL: secondServer.URL, Title: "same.bin"},
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(items))
	for _, item := range items {
		wg.Add(1)
		go func(item model.MediaItem) {
			defer wg.Done()
			_, err := dl.Download(item, dest)
			errs <- err
		}(item)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("download failed: %v", err)
		}
	}

	contents := make(map[string]bool)
	for _, name := range []string{"same.bin", "same (2).bin"} {
		data, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("expected collision-safe file %q: %v", name, err)
		}
		contents[string(data)] = true
	}
	if !contents["first"] || !contents["second"] {
		t.Fatalf("downloads overwrote each other: contents=%v", contents)
	}
}

func TestDownloadRejectsTruncatedChunkedResponse(t *testing.T) {
	// Flushing before writing the body prevents net/http from sniffing a
	// Content-Length, so the response really is chunked and the
	// scraper-reported item size is the only thing that can catch the
	// short body.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = w.Write(make([]byte, 50))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "chunked.bin", Size: 100}

	_, err := dl.Download(item, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected a size-mismatch error for a truncated chunked body, got %v", err)
	}
}

func TestDownloadAcceptsChunkedResponseMatchingItemSize(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "chunked-ok.bin", Size: 100}
	dest := t.TempDir()

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("chunked body matching the item size must succeed: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dest, "chunked-ok.bin")); err != nil || info.Size() != 100 {
		t.Fatalf("final file = %v, %v", info, err)
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

func TestShutdownCancelsInFlightDownload(t *testing.T) {
	requestStarted := make(chan struct{})
	var once sync.Once
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(requestStarted) })
		<-r.Context().Done() // block until the client aborts
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "slow.bin"}

	downloadDone := make(chan error, 1)
	go func() { _, err := dl.Download(item, t.TempDir()); downloadDone <- err }()
	<-requestStarted

	start := time.Now()
	dl.Shutdown()

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
	waitProgressClosed(t, dl.Progress(), 5*time.Second)
}

func TestShutdownUnblocksQueuedDownloads(t *testing.T) {
	firstStarted := make(chan struct{})
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			close(firstStarted)
		}
		<-r.Context().Done()
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	first := model.MediaItem{ID: "first", URL: ts.URL + "/first", Title: "first.bin"}
	second := model.MediaItem{ID: "second", URL: ts.URL + "/second", Title: "second.bin"}
	dest := t.TempDir()

	firstDone := make(chan error, 1)
	go func() { _, err := dl.Download(first, dest); firstDone <- err }()
	<-firstStarted

	secondDone := make(chan error, 1)
	go func() { _, err := dl.Download(second, dest); secondDone <- err }()

	// Wait until the second download is queued behind the semaphore.
	deadline := time.Now().Add(time.Second)
	for {
		dl.mu.Lock()
		_, waiting := dl.cancelMap[second.Key()]
		dl.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second download never entered the queue")
		}
		time.Sleep(time.Millisecond)
	}

	dl.Shutdown()
	for name, done := range map[string]chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "cancelled") {
				t.Errorf("%s download returned %v, want a cancellation", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s download did not return after Shutdown", name)
		}
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("queued download reached the server after Shutdown; requests=%d", got)
	}
}

func TestShutdownDeliversBufferedEventsBeforeClosing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "quick.bin"}
	if _, err := dl.Download(item, t.TempDir()); err != nil {
		t.Fatalf("download failed: %v", err)
	}

	dl.Shutdown()

	// Events the download emitted before Shutdown must still be delivered
	// to a consumer that only starts ranging after Shutdown returned.
	var sawCompleted bool
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-dl.Progress():
			if !ok {
				if !sawCompleted {
					t.Error("progress channel closed before the completed event was delivered")
				}
				return
			}
			if evt.Status == model.StatusCompleted {
				sawCompleted = true
			}
		case <-deadline:
			t.Fatal("progress channel was not closed after Shutdown")
		}
	}
}

func TestDownloadFailsFastAfterShutdown(t *testing.T) {
	dl := newTestDownloader(1, 0, "")
	dl.Shutdown()

	// The channel closes asynchronously; the fail-fast path must never
	// send on it (a send on a closed channel would panic).
	waitProgressClosed(t, dl.Progress(), 5*time.Second)

	start := time.Now()
	_, err := dl.Download(model.MediaItem{URL: "http://example.com/x", Title: "x"}, t.TempDir())
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

func TestShutdownIsIdempotent(t *testing.T) {
	dl := newTestDownloader(1, 0, "")
	dl.Shutdown()
	dl.Shutdown() // a second call must not block or close the channel twice
	waitProgressClosed(t, dl.Progress(), 5*time.Second)
}

func TestShutdownKeepsPartialFileForResume(t *testing.T) {
	gotBytes := make(chan struct{})
	var once sync.Once
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("PARTIAL8"))
		w.(http.Flusher).Flush()
		once.Do(func() { close(gotBytes) })
		<-r.Context().Done()
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "resume_shutdown.bin"}
	dest := t.TempDir()

	downloadDone := make(chan error, 1)
	go func() { _, err := dl.Download(item, dest); downloadDone <- err }()

	// Wait until the partial bytes have actually landed on disk.
	tempPath := filepath.Join(dest, "resume_shutdown.bin.part")
	<-gotBytes
	deadline := time.Now().Add(2 * time.Second)
	for {
		if info, err := os.Stat(tempPath); err == nil && info.Size() == 8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial bytes never landed on disk")
		}
		time.Sleep(5 * time.Millisecond)
	}

	dl.Shutdown()
	select {
	case err := <-downloadDone:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected a cancellation error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not cancel the in-flight download")
	}

	info, err := os.Stat(tempPath)
	if err != nil {
		t.Fatalf("interrupted download must keep its .part file for resume: %v", err)
	}
	if info.Size() != 8 {
		t.Errorf("expected .part to keep its 8 downloaded bytes, got %d", info.Size())
	}
}

// TestShutdownWithConcurrentDownloads hammers the sendProgress-vs-close
// race: many in-flight downloads emitting progress events while Shutdown
// cancels them and closes the channel. Run with -race; a send on a closed
// channel would panic.
func TestShutdownWithConcurrentDownloads(t *testing.T) {
	bodyStarted := make(chan struct{}, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			if _, err := w.Write(make([]byte, 1024)); err != nil {
				return // client went away
			}
			if i == 0 {
				bodyStarted <- struct{}{}
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer ts.Close()

	dl := newTestDownloader(4, 0, "")
	dest := t.TempDir()
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			item := model.MediaItem{
				ID:    fmt.Sprintf("%d", i),
				URL:   ts.URL,
				Title: fmt.Sprintf("file%d.bin", i),
			}
			_, err := dl.Download(item, dest)
			errs <- err
		}(i)
	}

	// Wait until every active slot has reached the response body.
	for i := 0; i < cap(bodyStarted); i++ {
		select {
		case <-bodyStarted:
		case <-time.After(time.Second):
			t.Fatal("concurrent download did not reach the response body")
		}
	}
	dl.Shutdown()

	waitProgressClosed(t, dl.Progress(), 10*time.Second)

	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("unexpected download error after Shutdown: %v", err)
		}
	}
}

// Long titles must survive the downloader's own suffixes: ".part" and
// ".part.resume.json" are appended while downloading, ".sha256" on success,
// and PathAllocator may add " (N)" for collisions.
func TestDownloadLongTitlesFitFilesystemLimits(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("long name content"))
	}))
	defer ts.Close()

	dest := t.TempDir()
	for name, title := range map[string]string{
		"ascii":   strings.Repeat("a", 300) + ".bin",
		"unicode": strings.Repeat("é", 200) + ".bin",
	} {
		t.Run(name, func(t *testing.T) {
			dl := newTestDownloader(1, 0, "")
			finalPath, err := dl.Download(model.MediaItem{URL: ts.URL, Title: title}, dest)
			if err != nil {
				t.Fatalf("download failed: %v", err)
			}
			if base := filepath.Base(finalPath); len(base) > 255 {
				t.Fatalf("final filename is %d bytes: %q", len(base), base)
			}
			if _, err := os.Stat(finalPath + ".sha256"); err != nil {
				t.Fatalf("expected checksum sidecar for %s: %v", finalPath, err)
			}
			if _, err := os.Stat(finalPath + ".part"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected the .part file to be gone after success")
			}
		})
	}
}

func TestDownloadRenameFailureEmitsFailedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer ts.Close()

	original := renameFile
	renameFile = func(oldpath, newpath string) error {
		return errors.New("rename blocked")
	}
	defer func() { renameFile = original }()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "rename.bin"}
	dest := t.TempDir()
	if _, err := dl.Download(item, dest); err == nil {
		t.Fatal("expected the rename failure to surface")
	}
	dl.Shutdown()

	var sawFailed bool
	for evt := range dl.Progress() {
		if evt.Status == model.StatusFailed {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Fatal("expected a StatusFailed progress event after a rename failure")
	}
	if _, err := os.Stat(filepath.Join(dest, "rename.bin.part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expected the .part file to be cleaned up after a rename failure")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "5", 5 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"http date", now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second},
		{"past date", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"garbage", "soon", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRetryAfter(tt.header, now); got != tt.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", tt.header, got, tt.want)
			}
		})
	}
}

// A 503 is retryable; with a resolver, the downloader must refresh the
// signed URL once instead of reusing the failing one.
func TestDownloadReResolvesOnRetryableStatus(t *testing.T) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Query().Get("sig") == "stale" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("fresh content"))
	}))
	defer ts.Close()

	var resolveCalls int32
	dl := newTestDownloader(1, 0, "")
	dl.SetSleeper(noopSleeper{})
	item := model.MediaItem{
		URL:   ts.URL + "/file",
		Title: "flaky.bin",
		Resolve: func(ctx context.Context) (string, error) {
			if atomic.AddInt32(&resolveCalls, 1) == 1 {
				return ts.URL + "/file?sig=stale", nil
			}
			return ts.URL + "/file?sig=fresh", nil
		},
	}

	dest := t.TempDir()
	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("expected re-resolve on 503 to recover, got: %v", err)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 2 {
		t.Errorf("resolve calls = %d, want 2 (initial + refresh)", got)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("HTTP requests = %d, want 2 (503 then success)", got)
	}
}

// A dropped connection must not leak the signed URL into the error.
func TestDownloadRedactsSignedURLFromConnectionError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dl.SetSleeper(noopSleeper{})
	item := model.MediaItem{
		URL:   ts.URL + "/placeholder",
		Title: "dropped.bin",
		Resolve: func(ctx context.Context) (string, error) {
			return ts.URL + "/signed?token=secret", nil
		},
	}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected the dropped connection to fail")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("connection error leaked the signed URL: %v", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected the error to wrap io.EOF, got: %v", err)
	}
}

func TestDownloadRefererHeader(t *testing.T) {
	tests := []struct {
		name    string
		referer string
		want    string
	}{
		{"set", "https://goonbox.cr/", "https://goonbox.cr/"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Referer")
				w.Write([]byte("ok"))
			}))
			defer ts.Close()

			dl := newTestDownloader(1, 0, "")
			item := model.MediaItem{URL: ts.URL + "/file.mp4", Title: "file.mp4", Referer: tt.referer}
			if _, err := dl.Download(item, t.TempDir()); err != nil {
				t.Fatalf("Download failed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Referer = %q, want %q", got, tt.want)
			}
		})
	}
}

type recordingSleeper struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *recordingSleeper) Sleep(time.Duration) {}

func (s *recordingSleeper) SleepCtx(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delays = append(s.delays, d)
	return nil
}

// A real 503 with Retry-After must flow through downloadOnce into the
// sleeper, and a huge hint must still be capped at netutil.MaxRetryDelay.
func TestDownloadHonorsRetryAfterFromResponse(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		wantMin    time.Duration
	}{
		{"seconds", "2", 2 * time.Second},
		{"huge capped", "1000000000", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if atomic.AddInt32(&requests, 1) == 1 {
					w.Header().Set("Retry-After", tt.retryAfter)
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Write([]byte("ok"))
			}))
			defer ts.Close()

			dl := newTestDownloader(1, 0, "")
			sleeper := &recordingSleeper{}
			dl.SetSleeper(sleeper)
			item := model.MediaItem{URL: ts.URL + "/file", Title: "retryafter.bin"}
			if _, err := dl.Download(item, t.TempDir()); err != nil {
				t.Fatalf("expected retry to succeed, got %v", err)
			}
			if len(sleeper.delays) != 1 {
				t.Fatalf("delays = %v, want exactly one", sleeper.delays)
			}
			if tt.wantMin > 0 && sleeper.delays[0] < tt.wantMin {
				t.Fatalf("delay = %s, want >= %s", sleeper.delays[0], tt.wantMin)
			}
			if sleeper.delays[0] > netutil.MaxRetryDelay {
				t.Fatalf("delay = %s, want <= %s", sleeper.delays[0], netutil.MaxRetryDelay)
			}
		})
	}
}

// A 404 is permanent even for items with a resolver: no URL refresh.
func TestDownload404WithResolverNotReResolved(t *testing.T) {
	var requests, resolveCalls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dl.SetSleeper(noopSleeper{})
	item := model.MediaItem{
		URL:   ts.URL + "/file",
		Title: "gone.bin",
		Resolve: func(ctx context.Context) (string, error) {
			atomic.AddInt32(&resolveCalls, 1)
			return ts.URL + "/file", nil
		},
	}
	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected a 404 to fail")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("HTTP requests = %d, want 1 (404 is permanent)", got)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Errorf("resolve calls = %d, want 1 (no refresh for 404)", got)
	}
}

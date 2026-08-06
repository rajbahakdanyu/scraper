// internal/downloader/downloader_test.go
package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

type noopSleeper struct{}

func (noopSleeper) Sleep(time.Duration)                           {}
func (noopSleeper) SleepCtx(context.Context, time.Duration) error { return nil }

type blockingSleeper struct {
	started chan struct{}
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
	err := dl.Download(item, dest)
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

	err := dl.Download(item, t.TempDir())
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
	go func() { done <- dl.Download(item, t.TempDir()) }()

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

	err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected error for failing download")
	}
}

func TestCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "cancel_test"}

	go func() {
		time.Sleep(100 * time.Millisecond)
		dl.Cancel(item)
	}()

	start := time.Now()
	err := dl.Download(item, t.TempDir())
	elapsed := time.Since(start)
	t.Logf("Download() returned after %v", elapsed)

	if err == nil {
		t.Fatal("expected error for cancelled download")
	}
	// The handler sleeps for 2s before responding. If Cancel only marked
	// the download as cancelled without aborting the in-flight request
	// (the request's context), Download would still block for the full
	// 2s. A real abort should return well before that. (Note: the test's
	// own reported duration below still includes ~2s because the deferred
	// ts.Close() blocks until the server's handler goroutine finishes
	// sleeping — that's httptest.Server's documented behavior, not
	// Download() itself; hence asserting on our own elapsed measurement.)
	if elapsed >= 1500*time.Millisecond {
		t.Errorf("expected cancel to abort the in-flight request quickly, took %v", elapsed)
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
	go func() { firstDone <- dl.Download(first, dest) }()
	<-firstStarted

	dl.Prepare(second)
	secondDone := make(chan error, 1)
	go func() { secondDone <- dl.Download(second, dest) }()

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
	err = dl.downloadOnce(context.Background(), item, item.URL, filepath.Join(t.TempDir(), "headers.bin.part"))
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

	err := dl.Download(item, t.TempDir())
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

	if err := dl.Download(item, dest); err != nil {
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

	if err := dl.Download(item, dest); err != nil {
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

	if err := dl.Download(item, dest); err != nil {
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

	if err := dl.Download(item, dest); err != nil {
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

	if err := dl.Download(item, dest); err != nil {
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

	if err := dl.Download(item, dest); err == nil {
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

	if err := dl.Download(item, dest); err == nil {
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
	if err := dl.Download(item, dest); err != nil {
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
	if err := dl.Download(item, dest); err != nil {
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

	err := dl.Download(item, t.TempDir())
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

	err := dl.Download(item, t.TempDir())
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

	err := dl.Download(item, t.TempDir())
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
	if err := dl.Download(item, dest); err != nil {
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

	err := dl.Download(item, t.TempDir())
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
	if err := dl.Download(item, dest); err == nil {
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

	if err := dl.Download(item, dest); err != nil {
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

	start := time.Now()
	if err := dl.Download(item, t.TempDir()); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("expected a 20KB/s cap on a 96KB file to take over a second, took %v", elapsed)
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
	if err := dl.Download(item, t.TempDir()); err != nil {
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

	if err := dl.Download(item, dest); err != nil {
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

func TestDownloadSkipsChecksumSidecarWhenDisabled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("no checksum"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	dl.SetSkipChecksums(true)
	item := model.MediaItem{URL: ts.URL, Title: "nosum.bin"}
	dest := t.TempDir()

	if err := dl.Download(item, dest); err != nil {
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

	if err := dl.Download(item, dest); err != nil {
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

	if err := dl.Download(item, dest); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Errorf("expected no request for a file matching its checksum, got %d", got)
	}
}

func TestDownloadRejectsMalformedChecksumSidecar(t *testing.T) {
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

	err := dl.Download(item, dest)
	if err == nil || !strings.Contains(err.Error(), "invalid SHA-256") {
		t.Fatalf("expected malformed checksum error, got %v", err)
	}
	if requests != 0 {
		t.Fatalf("malformed checksum unexpectedly triggered %d requests", requests)
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

	err := dl.Download(item, dest)
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
			errs <- dl.Download(item, dest)
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

	err := dl.Download(item, t.TempDir())
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

	if err := dl.Download(item, dest); err != nil {
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
	go func() { downloadDone <- dl.Download(item, t.TempDir()) }()
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
	go func() { firstDone <- dl.Download(first, dest) }()
	<-firstStarted

	secondDone := make(chan error, 1)
	go func() { secondDone <- dl.Download(second, dest) }()

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
	if err := dl.Download(item, t.TempDir()); err != nil {
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
	err := dl.Download(model.MediaItem{URL: "http://example.com/x", Title: "x"}, t.TempDir())
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
	go func() { downloadDone <- dl.Download(item, dest) }()

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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			if _, err := w.Write(make([]byte, 1024)); err != nil {
				return // client went away
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
			errs <- dl.Download(item, dest)
		}(i)
	}

	// Let the downloads get genuinely mid-body, then shut down.
	time.Sleep(50 * time.Millisecond)
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

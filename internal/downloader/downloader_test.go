// internal/downloader/downloader_test.go
package downloader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/scraper/internal/model"
)

func TestDownload(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dl := NewDownloaderWithConfig(5, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "retry_test"}

	err := dl.Download(item, t.TempDir())
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestDownloadAllFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

func TestNewDownloaderWithConfig(t *testing.T) {
	dl := NewDownloaderWithConfig(3, 10*time.Second, "CustomAgent/2.0")
	if dl.userAgent != "CustomAgent/2.0" {
		t.Errorf("expected CustomAgent/2.0, got %s", dl.userAgent)
	}
	if cap(dl.sem) != 3 {
		t.Errorf("expected concurrency 3, got %d", cap(dl.sem))
	}
}

func TestNewRequestError(t *testing.T) {
	dl := NewDownloaderWithConfig(1, 0, "")
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

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawRange = r.Header.Get("Range")
		mu.Unlock()
		w.Header().Set("Content-Range", "bytes 10-19/20")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(full[10:]))
	}))
	defer ts.Close()

	dl := NewDownloaderWithConfig(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "resume.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "resume.bin.part")
	if err := os.WriteFile(tempPath, []byte(full[:10]), 0644); err != nil {
		t.Fatalf("failed to seed partial file: %v", err)
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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignores any Range header and always returns the full content,
		// simulating a server that doesn't support range requests.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fresh))
	}))
	defer ts.Close()

	dl := NewDownloaderWithConfig(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "fallback.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "fallback.bin.part")
	if err := os.WriteFile(tempPath, []byte("STALEGARBAGE"), 0644); err != nil {
		t.Fatalf("failed to seed partial file: %v", err)
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

	dl := NewDownloaderWithConfig(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "restart416.bin"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "restart416.bin.part")
	if err := os.WriteFile(tempPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to seed partial file: %v", err)
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

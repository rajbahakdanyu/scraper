// internal/downloader/downloader_test.go
package downloader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

func TestSetRateLimitThrottlesThroughput(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 96*1024) // well over minRateLimitBurst
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer ts.Close()

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(1, 0, "")
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

	dl := NewDownloaderWithConfig(2, 0, "")
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

// internal/downloader/downloader_test.go
package downloader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	dl := NewDownloader(5)
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

	dl := NewDownloader(1)
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

	dl := NewDownloader(1)
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

	dl := NewDownloader(1)
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
	dl := NewDownloader(1)
	item := model.MediaItem{URL: "://bad-url", Title: "bad"}

	err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
	if !strings.Contains(err.Error(), "download failed") {
		t.Errorf("expected download failed error, got: %v", err)
	}
}

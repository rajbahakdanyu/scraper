// internal/downloader/downloader_test.go
package downloader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/rajbahakdanyu/scraper/internal/downloadercore"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestProgressReaderRateLimitedLargeRead(t *testing.T) {
	pr := &progressReader{
		reader:     bytes.NewReader(make([]byte, 1<<20)),
		onProgress: func(int64) {},
		limiter:    rate.NewLimiter(rate.Limit(1<<30), downloadercore.MinRateLimitBurst),
		ctx:        context.Background(),
	}
	n, err := pr.Read(make([]byte, 512*1024))
	if err != nil || n != 512*1024 {
		t.Fatalf("Read larger than the limiter burst = %d, %v", n, err)
	}
}

func TestProgressReaderKeepsBytesWhenWaitCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr := &progressReader{
		reader:     bytes.NewReader(make([]byte, 100)),
		onProgress: func(int64) {},
		limiter:    rate.NewLimiter(1, downloadercore.MinRateLimitBurst),
		ctx:        ctx,
	}
	pr.limiter.AllowN(time.Now(), downloadercore.MinRateLimitBurst)
	n, err := pr.Read(make([]byte, 100))
	if err == nil || n != 100 {
		t.Fatalf("cancelled wait must still report the consumed bytes, got %d, %v", n, err)
	}
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

func TestDownloadCreatesMissingDestination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dest := filepath.Join(t.TempDir(), "new", "nested")
	path, err := newTestDownloader(1, 0, "").Download(model.MediaItem{URL: ts.URL + "/a.bin", Title: "a.bin"}, dest)
	if err != nil {
		t.Fatalf("download into a missing directory failed: %v", err)
	}
	if path != filepath.Join(dest, "a.bin") {
		t.Fatalf("path = %q", path)
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

func TestNewDownloaderWithConfig(t *testing.T) {
	dl := newTestDownloader(3, 10*time.Second, "CustomAgent/2.0")
	if dl.userAgent != "CustomAgent/2.0" {
		t.Errorf("expected CustomAgent/2.0, got %s", dl.userAgent)
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
			// Binary body: the subject is the Referer header, and a
			// media-named item must carry real media bytes to survive
			// the content sniff/scan.
			body := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypisom\x00\x00\x02\x00isomiso2avc1mp41")...)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Referer")
				w.Write(body)
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

// errAfterDataBody returns its data together with a transport error on the
// first Read, then io.EOF, like a connection reset mid-body.
type errAfterDataBody struct{ done bool }

func (b *errAfterDataBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	b.done = true
	return copy(p, "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), io.ErrUnexpectedEOF
}

func (b *errAfterDataBody) Close() error { return nil }

func TestDownloadFailsWhenFirstReadReturnsDataAndError(t *testing.T) {
	dl := newTestDownloader(1, 0, "")
	dl.client = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{},
			Body:          &errAfterDataBody{},
			ContentLength: -1,
			Request:       r,
		}, nil
	})}
	dest := t.TempDir()
	item := model.MediaItem{URL: "https://cdn.example.com/clip.mp4", Title: "clip.mp4"}
	if _, err := dl.Download(item, dest); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Download error = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "clip.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a truncated body must not be committed, stat err = %v", err)
	}
}

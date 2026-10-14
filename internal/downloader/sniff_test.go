package downloader

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestDownloadRejectsHTMLMasqueradingAsMedia(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!DOCTYPE html><html><body>not a video</body></html>"))
	}))
	defer ts.Close()

	dest := t.TempDir()
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL + "/video.mp4", Title: "video.mp4"}

	_, err := dl.Download(item, dest)
	if err == nil {
		t.Fatal("expected HTML content to be refused")
	}
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("expected a non-retryable refusal, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("error should name the content type, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "video.mp4")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("refused payload must not be kept, stat err = %v", statErr)
	}
}

func TestDownloadRejectsPaddedHTMLMasqueradingAsMedia(t *testing.T) {
	// 4096 bytes of binary filler, then an HTML document: the
	// pre-transfer sniff sees only filler bytes and would classify the
	// body as application/octet-stream. The post-transfer scan must
	// still refuse it.
	body := bytes.Repeat([]byte{0xA5}, 4096)
	body = append(body, []byte("<html><body><h1>not a video</h1></body></html>")...)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}))
	defer ts.Close()

	dest := t.TempDir()
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL + "/video.mp4", Title: "video.mp4", Size: int64(len(body))}

	_, err := dl.Download(item, dest)
	if err == nil {
		t.Fatal("padded HTML masquerading as media must be refused")
	}
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("expected a non-retryable refusal, got %T: %v", err, err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "video.mp4")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("refused payload must not be kept, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "video.mp4.part")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("refused content must not leave a resumable .part, stat err = %v", statErr)
	}
}

func TestDownloadRejectsEmptyBodyForMediaItem(t *testing.T) {
	// A 200 with no bytes is a CDN glitch, not a file: recording it as a
	// success silently loses the download (real-world: 0-byte .mp4 files
	// that "completed" green). It must fail retryably instead.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
	}))
	defer ts.Close()

	dest := t.TempDir()
	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL + "/video.mp4", Title: "video.mp4"}
	_, err := dl.Download(item, dest)
	if err == nil {
		t.Fatal("an empty body for a media item must not be recorded as success")
	}
	if !strings.Contains(err.Error(), "empty response body") {
		t.Fatalf("error should name the empty body, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "video.mp4")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("no file may be kept for an empty body, stat err = %v", statErr)
	}
}

func TestDownloadAllowsHTMLForNonMediaItem(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!DOCTYPE html><html><body>a page</body></html>"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL + "/page.html", Title: "page.html"}
	if _, err := dl.Download(item, t.TempDir()); err != nil {
		t.Fatalf("an explicitly requested non-media item must still download: %v", err)
	}
}

func TestDownloadAllowsBinaryMedia(t *testing.T) {
	payload := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypisom\x00\x00\x02\x00isomiso2avc1mp41")...)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL + "/clip.mp4", Title: "clip.mp4"}
	path, err := dl.Download(item, t.TempDir())
	if err != nil {
		t.Fatalf("binary media must pass the content sniff: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch: got %d bytes", len(got))
	}
}

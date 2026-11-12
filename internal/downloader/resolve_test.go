package downloader

import (
	"context"
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

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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
// a model.NonRetryableError) fails without retries and without ever
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
			return "", model.NewNonRetryable(fmt.Errorf("file deleted upstream"))
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

// TestDownloadResumesAfterSignedURLRotation is the cross-run-resume win: an
// interrupted run leaves a .part plus resume metadata tied to an OLD
// signed URL; the next run resolves a FRESH signed URL for the SAME
// resource. The URL strings differ, but the stable ETag validator — not
// URL equality — is what protects resume integrity, so the partial must
// resume via a Range request instead of being discarded.
func TestDownloadDoesNotResumeUnverifiedResolvedPartial(t *testing.T) {
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
	if gotRange != "" {
		t.Errorf("unverified resolved partial must not be resumed, Range header = %q", gotRange)
	}
	if gotIfRange != "" {
		t.Errorf("unverified resolved partial must not be resumed, If-Range header = %q", gotIfRange)
	}
	got, err := os.ReadFile(filepath.Join(dest, "rotated (2).bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "re-downloaded-from-scratch" {
		t.Errorf("new copy = %q, want complete fresh response", got)
	}
	oldPartial, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldPartial) != full[:10] {
		t.Errorf("legacy partial was not preserved: %q", oldPartial)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Errorf("expected 1 resolve call, got %d", got)
	}
}

func TestDownloadResumesResolvedPartialWithMatchingIdentity(t *testing.T) {
	const full = "verified-partial-content"
	var rangeMu sync.Mutex
	var gotRange string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeMu.Lock()
		gotRange = r.Header.Get("Range")
		rangeMu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		if gotRange == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(full))
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 10-%d/%d", len(full)-1, len(full)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(full[10:]))
	}))
	defer ts.Close()

	item := model.MediaItem{
		URL:   ts.URL + "/page",
		Title: "verified.bin",
		Resolve: func(context.Context) (string, error) {
			return ts.URL + "/file?sig=fresh", nil
		},
	}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "verified.bin.part")
	if err := os.WriteFile(tempPath, []byte(full[:10]), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeResumeMetadata(tempPath, resumeMetadata{
		URL:          "https://cdn.example.com/file?sig=expired",
		ETag:         `"v1"`,
		ItemIdentity: itemIdentity(item),
	}); err != nil {
		t.Fatal(err)
	}

	dl := newTestDownloader(1, 0, "")
	path, err := dl.Download(item, dest)
	if err != nil {
		t.Fatalf("verified partial should resume: %v", err)
	}
	rangeMu.Lock()
	rangeHeader := gotRange
	rangeMu.Unlock()
	if rangeHeader != "bytes=10-" {
		t.Fatalf("Range header = %q, want bytes=10-", rangeHeader)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != full {
		t.Fatalf("final content = %q, want %q", got, full)
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

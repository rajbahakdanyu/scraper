package downloader

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// A resumed download must not re-sniff: the bytes a Range response starts
// with are mid-file, so they can look like anything and must not refuse a
// download whose start was already accepted.
func TestDownloadResumeSkipsContentSniff(t *testing.T) {
	const head = "0123456789"
	body := "<!DOCTYPE html><html><body>mid-file bytes</body></html>"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", len(head), len(head)+len(body)-1, len(head)+len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(body))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "resume.mp4"}
	dest := t.TempDir()
	tempPath := filepath.Join(dest, "resume.mp4.part")
	if err := os.WriteFile(tempPath, []byte(head), 0644); err != nil {
		t.Fatalf("failed to seed partial file: %v", err)
	}
	if err := writeResumeMetadata(tempPath, resumeMetadata{URL: ts.URL, ETag: `"v1"`}); err != nil {
		t.Fatalf("failed to seed resume metadata: %v", err)
	}

	if _, err := dl.Download(item, dest); err != nil {
		t.Fatalf("resumed download must not be refused by the content sniff: %v", err)
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

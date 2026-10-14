package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

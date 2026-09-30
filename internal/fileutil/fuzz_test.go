package fileutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzSanitizeFilename pins the invariants that keep a crafted title from
// escaping the output directory or breaking the filesystem.
func FuzzSanitizeFilename(f *testing.F) {
	for _, s := range []string{
		"clip.mp4", "..", "CON", "photo\u202Egnp.exe",
		strings.Repeat("x", 300), "a/b\\c", "video.mp4. ", "...", "\x00\x1f",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := SanitizeFilename(s)
		if len(got) > filenameBudget {
			t.Fatalf("SanitizeFilename(%q) = %d bytes, want <= %d", s, len(got), filenameBudget)
		}
		if strings.ContainsAny(got, `/\`) {
			t.Fatalf("SanitizeFilename(%q) kept a path separator: %q", s, got)
		}
		if got == "." || got == ".." {
			t.Fatalf("SanitizeFilename(%q) returned a path segment: %q", s, got)
		}
		if again := SanitizeFilename(got); again != got {
			t.Fatalf("SanitizeFilename not idempotent: %q -> %q -> %q", s, got, again)
		}
	})
}

// FuzzVerifyChecksum pins that no sidecar content can make verification
// error out (unparseable sidecars are treated as absent) and that garbage
// never reports a match.
func FuzzVerifyChecksum(f *testing.F) {
	for _, s := range []string{
		"",
		"abc123  file.bin\n",
		"not-a-hash  file.bin\n",
		"abc123 *file.bin\n",
		"abc123\n",
		"\x00\xff  file.bin\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, sidecar string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "file.bin")
		if err := os.WriteFile(path, []byte("payload"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ChecksumPath(path), []byte(sidecar), 0644); err != nil {
			t.Fatal(err)
		}
		present, matches, err := VerifyChecksum(path)
		if err != nil {
			t.Fatalf("VerifyChecksum(%q) returned an error: %v", sidecar, err)
		}
		if present && matches && strings.Contains(sidecar, "not-a-hash") {
			t.Fatalf("garbage sidecar %q reported as a match", sidecar)
		}
	})
}

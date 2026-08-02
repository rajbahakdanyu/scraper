package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChecksumRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteChecksum(path); err != nil {
		t.Fatal(err)
	}
	present, matches, err := VerifyChecksum(path)
	if err != nil || !present || !matches {
		t.Fatalf("verification = present %v matches %v err %v", present, matches, err)
	}
}

func TestVerifyChecksumAllowsMissingSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	present, matches, err := VerifyChecksum(path)
	if err != nil || present || matches {
		t.Fatalf("verification = present %v matches %v err %v", present, matches, err)
	}
}

func TestVerifyChecksumRejectsSidecarWrittenForDifferentFile(t *testing.T) {
	// The copied-folder trap: b.mp4 is a byte-for-byte copy of a.mp4 and
	// b.mp4.sha256 is a copy of a.mp4.sha256, so the hash matches b's
	// bytes but the sidecar names a.mp4. The sidecar says nothing about
	// b.mp4, so it must be reported as absent, not trusted.
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp4")
	b := filepath.Join(dir, "b.mp4")
	if err := os.WriteFile(a, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteChecksum(a); err != nil {
		t.Fatal(err)
	}
	sidecar, err := os.ReadFile(ChecksumPath(a))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ChecksumPath(b), sidecar, 0644); err != nil {
		t.Fatal(err)
	}
	present, matches, err := VerifyChecksum(b)
	if err != nil || present || matches {
		t.Fatalf("verification = present %v matches %v err %v", present, matches, err)
	}
}

func TestVerifyChecksumFilenameWithSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my file.bin")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteChecksum(path); err != nil {
		t.Fatal(err)
	}
	present, matches, err := VerifyChecksum(path)
	if err != nil || !present || !matches {
		t.Fatalf("verification = present %v matches %v err %v", present, matches, err)
	}
}

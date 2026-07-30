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

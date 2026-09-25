package fileutil

import (
	"os"
	"path/filepath"
	"strings"
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

func TestVerifyChecksumRejectsHashOnlySidecar(t *testing.T) {
	// A hash-only sidecar line (just the digest, no filename field at all)
	// carries zero evidence it was written for this file. Even when the
	// hash matches the file's bytes it must be reported as absent, the
	// same as a mismatched-name sidecar, so the caller re-verifies
	// instead of trusting an unattributed digest.
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteChecksum(path); err != nil {
		t.Fatal(err)
	}
	sidecar, err := os.ReadFile(ChecksumPath(path))
	if err != nil {
		t.Fatal(err)
	}
	hash, _, _ := strings.Cut(string(sidecar), " ")
	if err := os.WriteFile(ChecksumPath(path), []byte(hash+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	present, matches, err := VerifyChecksum(path)
	if err != nil || present || matches {
		t.Fatalf("verification = present %v matches %v err %v", present, matches, err)
	}
}

func TestVerifyChecksumAcceptsGNUBinaryMarker(t *testing.T) {
	// GNU "sha256sum -b" writes "<hash> *<name>": the mode marker between
	// the single separating space and the filename is '*' instead of ' '.
	// The parser must tolerate that form and still verify.
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteChecksum(path); err != nil {
		t.Fatal(err)
	}
	sidecar, err := os.ReadFile(ChecksumPath(path))
	if err != nil {
		t.Fatal(err)
	}
	hash, _, _ := strings.Cut(string(sidecar), " ")
	line := hash + " *" + filepath.Base(path) + "\n"
	if err := os.WriteFile(ChecksumPath(path), []byte(line), 0644); err != nil {
		t.Fatal(err)
	}
	present, matches, err := VerifyChecksum(path)
	if err != nil || !present || !matches {
		t.Fatalf("verification = present %v matches %v err %v", present, matches, err)
	}
}

func TestVerifyChecksumTreatsUnparseableSidecarAsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	validDigest := strings.Repeat("ab", 32) // 64 hex chars

	for name, content := range map[string]string{
		"empty":          "",
		"whitespace":     "  \n\n",
		"CRLF blank":     "\r\n",
		"not a digest":   "not-a-digest\n",
		"tab separated":  validDigest + "\tfile.bin\n",
		"short digest":   "abcd  file.bin\n",
		"non-hex digest": strings.Repeat("zz", 32) + "  file.bin\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(ChecksumPath(path), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			present, matches, err := VerifyChecksum(path)
			if err != nil || present || matches {
				t.Fatalf("verification = present %v matches %v err %v, want false/false/nil", present, matches, err)
			}
		})
	}
}

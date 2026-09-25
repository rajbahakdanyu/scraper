package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const checksumSuffix = ".sha256"

func ChecksumPath(path string) string {
	return path + checksumSuffix
}

func WriteChecksum(path string) error {
	sum, err := sha256File(path)
	if err != nil {
		return err
	}
	return WriteChecksumDigest(path, sum)
}

// WriteChecksumDigest writes a checksum sidecar from an already-computed
// SHA-256 digest, avoiding a second read of path.
func WriteChecksumDigest(path, sum string) error {
	line := fmt.Sprintf("%s  %s\n", sum, filepath.Base(path))
	sidecarPath := ChecksumPath(path)
	temp, err := os.CreateTemp(filepath.Dir(sidecarPath), ".checksum-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0644); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.WriteString(line); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Sync(); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, sidecarPath)
}

// VerifyChecksum reports whether a usable .sha256 sidecar exists for path
// and whether it matches the file's bytes. A missing sidecar, a sidecar
// written for a different file, and unparseable/truncated content are all
// reported as absent (present=false) rather than as errors, so callers
// never permanently fail a file because of a bad sidecar.
func VerifyChecksum(path string) (present, matches bool, err error) {
	data, err := os.ReadFile(ChecksumPath(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return true, false, err
	}
	// Parse the first line in sha256sum format: "<hex> <mode><filename>",
	// where <mode> is a marker character — ' ' (text, what WriteChecksum
	// writes) or '*' (binary). The filename may contain spaces, so split
	// only on the first space instead of fields-splitting.
	content := strings.TrimSpace(string(data))
	if content == "" {
		// An empty/whitespace-only sidecar is a truncated remnant (e.g.
		// disk-full during write), not evidence the file is bad. Treat it
		// exactly like a missing or wrong-name sidecar: absent. A hard
		// error here would permanently fail an otherwise complete file.
		return false, false, nil
	}
	line, _, _ := strings.Cut(content, "\n")
	line = strings.TrimSpace(line)
	digest, rest, _ := strings.Cut(line, " ")
	expected, err := hex.DecodeString(digest)
	if err != nil || len(expected) != sha256.Size {
		// Same trust model as above: unparseable content carries no usable
		// evidence, so report it absent instead of failing hard.
		return false, false, nil
	}
	// rest is "<mode><filename>"; fewer than two characters means no
	// filename was recorded (hash-only line, or a mode marker with no
	// name). Such a sidecar carries zero evidence it was written for this
	// file, so it is treated exactly like one that names a different file
	// — base names compared, since the sidecar stores the name as written
	// at creation (WriteChecksum writes the basename) — i.e. written for
	// something else (renamed file, copied folder) or for nothing
	// identifiable at all. Report it as absent, the same as a missing
	// sidecar, so the caller re-verifies rather than trusting a stale
	// association or failing hard.
	if len(rest) <= 1 || filepath.Base(rest[1:]) != filepath.Base(path) {
		return false, false, nil
	}
	got, err := sha256File(path)
	if err != nil {
		return true, false, err
	}
	return true, strings.EqualFold(got, digest), nil
}

func sha256File(path string) (string, error) {
	// #nosec G304 -- callers pass internally allocated download paths for local integrity checks.
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

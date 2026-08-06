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
		return true, false, fmt.Errorf("empty checksum sidecar")
	}
	line, _, _ := strings.Cut(content, "\n")
	line = strings.TrimSpace(line)
	digest, rest, _ := strings.Cut(line, " ")
	expected, err := hex.DecodeString(digest)
	if err != nil || len(expected) != sha256.Size {
		return true, false, fmt.Errorf("invalid SHA-256 digest %q", digest)
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

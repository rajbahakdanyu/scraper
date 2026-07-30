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
		temp.Close()
		return err
	}
	if _, err := temp.WriteString(line); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
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
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return true, false, fmt.Errorf("empty checksum sidecar")
	}
	expected, err := hex.DecodeString(fields[0])
	if err != nil || len(expected) != sha256.Size {
		return true, false, fmt.Errorf("invalid SHA-256 digest %q", fields[0])
	}
	got, err := sha256File(path)
	if err != nil {
		return true, false, err
	}
	return true, strings.EqualFold(got, fields[0]), nil
}

func sha256File(path string) (string, error) {
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

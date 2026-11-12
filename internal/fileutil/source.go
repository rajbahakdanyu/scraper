package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const sourceMetadataSuffix = ".source.json"

type sourceMetadata struct {
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

func SourceMetadataPath(path string) string {
	return path + sourceMetadataSuffix
}

// VerifySource reports whether path is a complete, checksummed file previously
// written for identity. Missing or malformed metadata is unverified.
func VerifySource(path, identity string, expectedSize int64) (bool, error) {
	data, err := ReadFile(SourceMetadataPath(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var metadata sourceMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return false, nil
	}
	if metadata.Identity == "" || metadata.Identity != identity || metadata.SHA256 == "" {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.Size() == 0 || info.Size() != metadata.Size ||
		(expectedSize > 0 && info.Size() != expectedSize) {
		return false, nil
	}
	digest, err := sha256File(path)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(digest, metadata.SHA256), nil
}

func WriteSource(path, identity, digest string) error {
	if identity == "" || digest == "" {
		return errors.New("source identity and digest are required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(sourceMetadata{
		Identity: identity,
		Size:     info.Size(),
		SHA256:   digest,
	})
	if err != nil {
		return err
	}
	target := SourceMetadataPath(path)
	temp, err := os.CreateTemp(filepath.Dir(target), ".source-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0600); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.Write(metadata); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Sync(); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("replace source metadata: %w", err)
	}
	return nil
}

func SourceIdentity(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

func DigestFile(path string) (string, error) {
	return sha256File(path)
}

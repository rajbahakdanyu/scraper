package downloader

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
)

func parseContentRange(headerVal string) (start, end, total int64, err error) {
	if _, scanErr := fmt.Sscanf(headerVal, "bytes %d-%d/%d", &start, &end, &total); scanErr != nil {
		return 0, 0, 0, scanErr
	}
	if start < 0 || end < start || total <= end {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range %q", headerVal)
	}
	return start, end, total, nil
}

// parseContentRangeTotal parses the "bytes */<total>" form of Content-Range
// that accompanies a 416 response, returning just the resource's total size.
func parseContentRangeTotal(headerVal string) (int64, error) {
	var total int64
	if _, err := fmt.Sscanf(headerVal, "bytes */%d", &total); err != nil {
		return 0, err
	}
	if total < 0 {
		return 0, fmt.Errorf("invalid Content-Range %q", headerVal)
	}
	return total, nil
}

type resumeMetadata struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

func metadataFromResponse(rawURL string, resp *http.Response, storeURL bool) resumeMetadata {
	etag := resp.Header.Get("ETag")
	if strings.HasPrefix(etag, "W/") {
		etag = ""
	}
	url := ""
	if storeURL {
		url = rawURL
	}
	return resumeMetadata{
		URL:          url,
		ETag:         etag,
		LastModified: resp.Header.Get("Last-Modified"),
	}
}

func (m resumeMetadata) validator() string {
	if m.ETag != "" {
		return m.ETag
	}
	return m.LastModified
}

func (m resumeMetadata) matches(resp *http.Response) bool {
	if m.ETag != "" {
		return resp.Header.Get("ETag") == "" || resp.Header.Get("ETag") == m.ETag
	}
	return resp.Header.Get("Last-Modified") == "" || resp.Header.Get("Last-Modified") == m.LastModified
}

func resumeMetadataPath(tempPath string) string {
	return tempPath + ".resume.json"
}

func readResumeMetadata(tempPath string) (resumeMetadata, error) {
	data, err := fileutil.ReadFile(resumeMetadataPath(tempPath))
	if err != nil {
		return resumeMetadata{}, err
	}
	var metadata resumeMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return resumeMetadata{}, err
	}
	return metadata, nil
}

func writeResumeMetadata(tempPath string, metadata resumeMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	target := resumeMetadataPath(tempPath)
	// Write to a temp file and rename into place: rename replaces a symlink
	// instead of writing through it, so a planted link cannot redirect the
	// write at an arbitrary victim file (same defense as the checksum sidecar).
	temp, err := os.CreateTemp(filepath.Dir(target), ".resume-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0600); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.Write(data); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, target)
}

func removeFiles(paths ...string) error {
	var errs []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

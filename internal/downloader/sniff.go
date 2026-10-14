package downloader

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// contentSniffLen is the byte count http.DetectContentType inspects.
const contentSniffLen = 512

// deniedContentTypes maps sniffed content types the downloader refuses when
// the item claims to be media: HTML and executables renamed to look like
// media. Archives and unknown binary types are not denied.
var deniedContentTypes = map[string]string{
	"text/html": "HTML",
	"application/vnd.microsoft.portable-executable": "an executable",
	"application/x-msdownload":                      "an executable",
	"application/x-dosexec":                         "an executable",
	"application/x-elf":                             "an executable",
	"application/x-mach-binary":                     "an executable",
	"application/x-executable":                      "an executable",
}

// deniedContentType returns a human label when the sniffed bytes look like
// content the downloader refuses, or "" when they are acceptable.
func deniedContentType(head []byte) string {
	if len(head) == 0 {
		return ""
	}
	ct := http.DetectContentType(head)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return deniedContentTypes[ct]
}

// suspiciousContentMarkers are byte signatures that cannot legitimately
// appear near the head of a media or archive file. Windows PE ("MZ") is
// deliberately not listed: two-byte magics false-positive on compressed
// media data; offset-0 PE is still caught by the pre-transfer sniff.
var suspiciousContentMarkers = [][]byte{
	[]byte("<!doctype html"),
	[]byte("<html"),
	[]byte("\x7fELF"),          // ELF
	[]byte("\xcf\xfa\xed\xfe"), // Mach-O 64
	[]byte("\xce\xfa\xed\xfe"), // Mach-O 64 (byte-swapped)
	[]byte("\xfe\xed\xfa\xce"), // Mach-O 32
	[]byte("\xca\xfe\xba\xbe"), // Mach-O fat / Java class
}

// refuseSuspiciousContent scans the head of a freshly downloaded temp
// file for content the downloader refuses under a media name. Unlike the
// pre-transfer sniff, padding the front of an HTML or executable payload
// cannot hide it here. Resumed downloads are exempt, like the
// pre-transfer sniff: a resumed Range response begins mid-file, where
// any on-disk bytes are trusted (recorded decision).
func refuseSuspiciousContent(tempPath string, item model.MediaItem, rawURL string) error {
	if !model.IsAllowedMediaItem(item) {
		return nil
	}
	f, err := os.Open(tempPath) // #nosec G304 -- path allocated by the downloader
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 64<<10)
	n, readErr := io.ReadFull(f, head)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return readErr
	}
	head = head[:n]
	if n == 0 {
		// An empty body carries nothing to classify; mirror the
		// pre-transfer sniff, which tolerates an empty head.
		return nil
	}

	sniffed := head
	if len(sniffed) > contentSniffLen {
		sniffed = sniffed[:contentSniffLen]
	}
	ct := http.DetectContentType(sniffed)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if strings.HasPrefix(ct, "text/") {
		return model.NewNonRetryable(fmt.Errorf("content is %s, not media: %s", ct, redactURL(rawURL)))
	}
	for _, marker := range suspiciousContentMarkers {
		if bytes.Contains(head, marker) {
			return model.NewNonRetryable(fmt.Errorf("content looks like %s, not media: %s", markerLabel(marker), redactURL(rawURL)))
		}
	}
	return nil
}

func markerLabel(marker []byte) string {
	if bytes.HasPrefix(marker, []byte("<")) {
		return "HTML"
	}
	return "an executable"
}

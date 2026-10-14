package downloader

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// extFromURL extracts the file extension from a URL's path component.
// Returns "" if the URL has no path or the path has no extension.
func extFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return path.Ext(u.Path)
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<redacted>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// parseRetryAfter interprets a Retry-After header (seconds or HTTP-date)
// as a delay from now. It returns 0 for absent, unparsable, or past values.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if d := when.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// redactConnectionError replaces the request URL inside a transport error
// with its redacted form so signed query strings never reach logs or
// failure reports. errors.Is/As still unwrap to the original cause.
func redactConnectionError(err error, rawURL string) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		clone := *ue
		clone.URL = redactURL(rawURL)
		return &clone
	}
	return err
}

// shouldRefreshURL reports whether an attempt failure should trigger one
// re-resolve of a signed URL: expired signatures (403), retryable HTTP
// statuses (429/5xx), and connection-level drops.
func shouldRefreshURL(err error) bool {
	var nre *model.NonRetryableError
	if errors.As(err, &nre) && nre.StatusCode == http.StatusForbidden {
		return true
	}
	var hse *model.HTTPStatusError
	if errors.As(err, &hse) {
		return true
	}
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET)
}

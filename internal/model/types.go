package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MediaType represents the type of media being downloaded
type MediaType string

const (
	MediaTypeVideo MediaType = "video"
	MediaTypeImage MediaType = "image"
)

// DownloadStatus represents the status of a download operation
type DownloadStatus int

const (
	StatusPending DownloadStatus = iota
	StatusDownloading
	StatusCompleted
	StatusFailed
	StatusCancelled
)

// MediaItem represents a single media item to be downloaded
type MediaItem struct {
	ID  string // Per-run identity used to distinguish duplicate URLs
	URL string // URL of the media resource (a stable page URL when Resolve is set)
	// PageURL is the human-facing page for the item when it differs from
	// URL (e.g. a bunkr /f/<slug> page). Retry reports prefer it so the
	// item can be re-scraped by feeding the URL back to the CLI.
	PageURL string
	// Referer, when non-empty, is sent as the Referer header for this
	// item's download; some CDNs (goonbox's image hosts) reject hotlinks
	// with HTTP 403 without it.
	Referer  string
	Title    string    // Display title of the media
	Size     int64     // Size of the media in bytes
	MimeType string    // MIME type of the media
	Type     MediaType // Type of media (video/image/etc.)
	// Resolve, when non-nil, produces the final download URL lazily at
	// download time instead of URL being that final URL. Parsers set this
	// for signed/expiring URLs: resolving a whole album eagerly guarantees
	// expired signatures by the time later items download, and a stale
	// signature breaks cross-run resume. URL then holds a stable,
	// meaningful placeholder (e.g. the file's page URL) used for identity
	// and display only. Resolve must be safe to call more than once (the
	// downloader re-resolves after a 403) and from concurrent download
	// goroutines. A returned error is retried with backoff unless it is a
	// *NonRetryableError, which fails the item immediately.
	Resolve func(ctx context.Context) (string, error)
}

type CancelledError struct{ URL string }

func (e *CancelledError) Error() string { return "download cancelled: " + e.URL }

func IsCancelled(err error) bool {
	var ce *CancelledError
	return errors.As(err, &ce)
}

func (m MediaItem) Key() string {
	if m.ID != "" {
		return m.ID
	}
	return m.URL
}

func TitleOf(item MediaItem) string {
	if item.Title != "" {
		return item.Title
	}
	return item.URL
}

func AssignIDs(items []MediaItem, prefix string) {
	for i := range items {
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("%s%d:%s", prefix, i, items[i].URL)
		}
	}
}

// Scraper interface defines the contract for site-specific scraping logic
type Scraper interface {
	Name() string                                               // Returns the logical name of the scraper
	Parse(ctx context.Context, url string) ([]MediaItem, error) // Parses the URL and extracts media items
}

type PartialError struct {
	Operation string
	Errors    []error
}

func NewPartialError(operation string, errs []error) error {
	filtered := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &PartialError{Operation: operation, Errors: filtered}
}

func (e *PartialError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("%s incomplete", e.Operation)
	}
	return fmt.Sprintf("%s incomplete: %d operation(s) failed; first error: %v", e.Operation, len(e.Errors), e.Errors[0])
}

func (e *PartialError) Unwrap() []error {
	return e.Errors
}

// ProgressEvent carries progress information for tracking downloads
type ProgressEvent struct {
	Item      MediaItem      // The media item being downloaded
	Bytes     int64          // Bytes downloaded so far
	Total     int64          // Total size of the media
	Status    DownloadStatus // Current download status
	Filename  string         // Target filename for the download
	Start     time.Time      // Start time of the download
	Quality   string         // YouTube quality cap, e.g. "1080p"
	AudioOnly string         // YouTube audio-only format, e.g. "m4a"
}

// MediaTypeForName infers the media type from a filename's extension.
func MediaTypeForName(name string) MediaType {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".jpg"),
		strings.HasSuffix(lower, ".jpeg"),
		strings.HasSuffix(lower, ".png"),
		strings.HasSuffix(lower, ".gif"),
		strings.HasSuffix(lower, ".webp"):
		return MediaTypeImage
	default:
		return MediaTypeVideo
	}
}

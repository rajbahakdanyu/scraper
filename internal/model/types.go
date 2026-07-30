package model

import (
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
	StatusPending     DownloadStatus = iota
	StatusDownloading DownloadStatus = iota
	StatusCompleted   DownloadStatus = iota
	StatusFailed      DownloadStatus = iota
	StatusCancelled   DownloadStatus = iota
)

// MediaItem represents a single media item to be downloaded
type MediaItem struct {
	ID        string    // Per-run identity used to distinguish duplicate URLs
	URL       string    // URL of the media resource
	Title     string    // Display title of the media
	Size      int64     // Size of the media in bytes
	MimeType  string    // MIME type of the media
	Type      MediaType // Type of media (video/image/etc.)
	Thumbnail string    // URL or path to thumbnail preview
}

func (m MediaItem) Key() string {
	if m.ID != "" {
		return m.ID
	}
	return m.URL
}

// Scraper interface defines the contract for site-specific scraping logic
type Scraper interface {
	Name() string                          // Returns the logical name of the scraper
	Parse(url string) ([]MediaItem, error) // Parses the URL and extracts media items
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
	return fmt.Sprintf("%s incomplete: %d operation(s) failed; first error: %v", e.Operation, len(e.Errors), e.Errors[0])
}

func (e *PartialError) Unwrap() []error {
	return e.Errors
}

// ProgressEvent carries progress information for tracking downloads
type ProgressEvent struct {
	Item     MediaItem      // The media item being downloaded
	Bytes    int64          // Bytes downloaded so far
	Total    int64          // Total size of the media
	Speed    float64        // Current download speed in bytes/sec
	Status   DownloadStatus // Current download status
	Filename string         // Target filename for the download
	Start    time.Time      // Start time of the download
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

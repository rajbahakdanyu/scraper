package model

import "time"

// MediaType represents the type of media being downloaded
type MediaType string

const (
	MediaTypeVideo MediaType = "video"
	MediaTypeImage MediaType = "image"
)

// DownloadStatus represents the status of a download operation
type DownloadStatus int

const (
	StatusPending         DownloadStatus = iota
	StatusDownloading     DownloadStatus = iota
	StatusCompleted       DownloadStatus = iota
	StatusFailed          DownloadStatus = iota
	StatusCancelled       DownloadStatus = iota
)

// MediaItem represents a single media item to be downloaded
type MediaItem struct {
	URL      string        // URL of the media resource
	Title    string        // Display title of the media
	Size     int64         // Size of the media in bytes
	MimeType string        // MIME type of the media
	Type     MediaType     // Type of media (video/image/etc.)
	Thumbnail string        // URL or path to thumbnail preview
}

// Scraper interface defines the contract for site-specific scraping logic
type Scraper interface {
	Name() string                 // Returns the logical name of the scraper
	Parse(url string) ([]MediaItem, error) // Parses the URL and extracts media items
}

// Downloader interface defines the contract for downloading media items
type Downloader interface {
	Download(item MediaItem, dest string) error // Downloads a single media item
	Cancel(item MediaItem)                     // Cancels an active download
}

// ProgressEvent carries progress information for tracking downloads
type ProgressEvent struct {
	Item     MediaItem        // The media item being downloaded
	Bytes    int64            // Bytes downloaded so far
	Total    int64            // Total size of the media
	Speed    float64          // Current download speed in bytes/sec
	Status   DownloadStatus   // Current download status
	Filename string           // Target filename for the download
	Start    time.Time        // Start time of the download
}
package model

// Downloader is the download backend every frontend (TUI, plain, JSON, GUI)
// drives.
type Downloader interface {
	Prepare(item MediaItem)
	Download(item MediaItem, dest string) (string, error)
	Cancel(item MediaItem)
	// Shutdown cancels every in-flight download and closes the progress
	// channel once they have unwound.
	Shutdown()
	Progress() <-chan ProgressEvent
}

// Album is one URL of a batch run: the orchestrator scrapes albums in order
// and downloads each into Dest.
type Album struct {
	Site    string
	URL     string
	Slug    string
	Dest    string
	Scraper Scraper
}

// FailedItem pairs a media item that did not download with the error that
// ended it and the album URL it was scraped from.
type FailedItem struct {
	Item     MediaItem
	Err      error
	AlbumURL string
}

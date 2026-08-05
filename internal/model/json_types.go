package model

type JSONItem struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Type     string `json:"type,omitempty"`
}

type JSONAlbum struct {
	Site  string     `json:"site"`
	URL   string     `json:"url"`
	Slug  string     `json:"slug"`
	Items []JSONItem `json:"items"`
}

type JSONSummary struct {
	Total       int   `json:"total"`
	TotalBytes  int64 `json:"totalBytes"`
	UnknownSize int   `json:"unknownSize,omitempty"`
}

type JSONEvent struct {
	Event      string `json:"event"`
	Site       string `json:"site,omitempty"`
	URL        string `json:"url,omitempty"`
	Slug       string `json:"slug,omitempty"`
	Title      string `json:"title,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Path       string `json:"path,omitempty"`
	Status     string `json:"status,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	MediaType  string `json:"type,omitempty"`
	Error      string `json:"error,omitempty"`
	Album      string `json:"album,omitempty"`
	Completed  int    `json:"completed,omitempty"`
	Failed     int    `json:"failed,omitempty"`
	Cancelled  int    `json:"cancelled,omitempty"`
	Total      int    `json:"total,omitempty"`
	TotalBytes int64  `json:"totalBytes,omitempty"`
	Quality    string `json:"quality,omitempty"`
	AudioOnly  string `json:"audioOnly,omitempty"`
}

func DownloadStatusString(s DownloadStatus) string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusDownloading:
		return "downloading"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func MediaTypeString(t MediaType) string {
	return string(t)
}

func ItemToJSONItem(item MediaItem) JSONItem {
	title := item.Title
	if title == "" {
		title = item.URL
	}
	return JSONItem{
		Title:    title,
		URL:      item.URL,
		Size:     item.Size,
		MimeType: item.MimeType,
		Type:     MediaTypeString(item.Type),
	}
}

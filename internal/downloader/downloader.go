// internal/downloader/downloader.go
package downloader

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/scraper/internal/model"
)

type Downloader struct {
	client      *http.Client
	concurrency int
	progress    chan model.ProgressEvent
	cancelMap   map[string]chan struct{}
	mu          sync.Mutex
	sem         chan struct{}
}

func NewDownloader(concurrency int) *Downloader {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Downloader{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		concurrency: concurrency,
		progress:    make(chan model.ProgressEvent, 100),
		cancelMap:   make(map[string]chan struct{}),
		sem:         make(chan struct{}, concurrency),
	}
}

func (d *Downloader) Download(item model.MediaItem, dest string) error {
	// Block until a concurrency slot is free so at most d.concurrency
	// downloads run at once, regardless of how many callers invoke
	// Download concurrently.
	d.sem <- struct{}{}
	defer func() { <-d.sem }()

	cancel := make(chan struct{})
	d.mu.Lock()
	d.cancelMap[item.URL] = cancel
	d.mu.Unlock()

	req, _ := http.NewRequest("GET", item.URL, nil)

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	filename := d.sanitizeFilename(item.Title + filepath.Ext(item.URL))
	if filename == "" {
		filename = "download"
	}
	path := filepath.Join(dest, filename)

	d.progress <- model.ProgressEvent{
		Item:   item,
		Status: model.StatusDownloading,
		Total:  resp.ContentLength,
		Start:  time.Now(),
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	d.progress <- model.ProgressEvent{
		Item:     item,
		Status:   model.StatusCompleted,
		Filename: path,
	}

	return nil
}

func (d *Downloader) Progress() <-chan model.ProgressEvent {
	return d.progress
}

func (d *Downloader) sanitizeFilename(s string) string {
	// Remove/replace invalid characters
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 32 && r != ':' && r != '<' && r != '>' && r != '|' && r != '/' && r != '\\' && r != '?' && r != '*' {
			clean = append(clean, r)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	return string(clean)
}
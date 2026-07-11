// internal/downloader/downloader.go
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/scraper/internal/model"
)

const (
	maxRetries      = 3
	retryBaseDelay  = 500 * time.Millisecond
	progressBufSize = 4096
)

// Downloader manages concurrent media downloads with progress tracking,
// cancellation, retry, and atomic file writes.
type Downloader struct {
	client      *http.Client
	concurrency int
	userAgent   string
	progress    chan model.ProgressEvent
	cancelMap   map[string]context.CancelFunc
	mu          sync.Mutex
	sem         chan struct{}
}

// NewDownloader creates a Downloader with the given concurrency limit.
func NewDownloader(concurrency int) *Downloader {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Downloader{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		concurrency: concurrency,
		userAgent:   "Scraper/1.0",
		progress:    make(chan model.ProgressEvent, progressBufSize),
		cancelMap:   make(map[string]context.CancelFunc),
		sem:         make(chan struct{}, concurrency),
	}
}

// NewDownloaderWithConfig creates a Downloader using settings from a config.
func NewDownloaderWithConfig(concurrency int, timeout time.Duration, userAgent string) *Downloader {
	if concurrency < 1 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if userAgent == "" {
		userAgent = "Scraper/1.0"
	}
	return &Downloader{
		client: &http.Client{
			Timeout: timeout,
		},
		concurrency: concurrency,
		userAgent:   userAgent,
		progress:    make(chan model.ProgressEvent, progressBufSize),
		cancelMap:   make(map[string]context.CancelFunc),
		sem:         make(chan struct{}, concurrency),
	}
}

// Download fetches a single media item and saves it to dest. It uses a
// temporary file during download and renames on success, so partial files
// are never left behind. Retries up to maxRetries times on transient errors.
func (d *Downloader) Download(item model.MediaItem, dest string) error {
	d.sem <- struct{}{}
	defer func() { <-d.sem }()

	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.cancelMap[item.URL] = cancel
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.cancelMap, item.URL)
		d.mu.Unlock()
		cancel()
	}()

	filename := sanitizeFilename(item.Title)
	if filename == "" {
		filename = "download"
	}
	if ext := extFromURL(item.URL); ext != "" {
		filename += ext
	}
	finalPath := filepath.Join(dest, filename)
	tempPath := finalPath + ".part"

	// Skip if file already exists and is non-empty
	if info, err := os.Stat(finalPath); err == nil && info.Size() > 0 {
		d.sendProgress(model.ProgressEvent{
			Item:     item,
			Status:   model.StatusCompleted,
			Filename: finalPath,
		})
		return nil
	}

	d.sendProgress(model.ProgressEvent{
		Item:   item,
		Status: model.StatusDownloading,
		Start:  time.Now(),
	})

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
				return fmt.Errorf("download cancelled: %s", item.URL)
			case <-time.After(retryBaseDelay << attempt):
			}
		}

		err := d.downloadOnce(ctx, item, tempPath)
		if err == nil {
			if err := os.Rename(tempPath, finalPath); err != nil {
				os.Remove(tempPath)
				return err
			}
			d.sendProgress(model.ProgressEvent{
				Item:     item,
				Status:   model.StatusCompleted,
				Filename: finalPath,
			})
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			// Cancelled mid-attempt: stop retrying instead of sleeping
			// through further backoff delays.
			break
		}
	}

	os.Remove(tempPath)
	if ctx.Err() != nil {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		return fmt.Errorf("download cancelled: %s", item.URL)
	}
	d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
	return fmt.Errorf("download failed after %d attempts: %w", maxRetries, lastErr)
}

// downloadOnce performs a single download attempt to tempPath. The request
// and the response body read are both bound to ctx, so cancelling ctx
// aborts an in-flight attempt immediately instead of waiting for it to
// finish.
func (d *Downloader) downloadOnce(ctx context.Context, item model.MediaItem, tempPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
	if err != nil {
		return err
	}
	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, item.URL)
	}

	total := resp.ContentLength
	if total < 0 {
		total = 0
	}

	out, err := os.Create(tempPath)
	if err != nil {
		return err
	}

	pr := &progressReader{
		reader: resp.Body,
		onProgress: func(bytes int64) {
			d.sendProgress(model.ProgressEvent{
				Item:   item,
				Bytes:  bytes,
				Total:  total,
				Status: model.StatusDownloading,
			})
		},
	}

	_, err = io.Copy(out, pr)
	out.Close()
	if err != nil {
		return err
	}

	return nil
}

// progressReader wraps an io.Reader and calls onProgress after each Read
// with the cumulative number of bytes read.
type progressReader struct {
	reader     io.Reader
	read       int64
	onProgress func(bytes int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.read += int64(n)
		pr.onProgress(pr.read)
	}
	return n, err
}

// Cancel cancels an active download for the given item. This cancels the
// download's request context, which aborts an in-flight HTTP request and
// interrupts an in-progress body read right away rather than waiting for
// the current attempt to finish.
func (d *Downloader) Cancel(item model.MediaItem) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cancel, ok := d.cancelMap[item.URL]; ok {
		cancel()
		delete(d.cancelMap, item.URL)
	}
}

// Progress returns a read-only channel of download progress events.
func (d *Downloader) Progress() <-chan model.ProgressEvent {
	return d.progress
}

// sendProgress sends an event. Uses a large buffer (4096) so events
// are rarely dropped. If the buffer is full the event is dropped to
// prevent stalling the download goroutine.
func (d *Downloader) sendProgress(evt model.ProgressEvent) {
	select {
	case d.progress <- evt:
	default:
	}
}

// sanitizeFilename removes characters that are invalid in filenames. It also
// rejects "." and ".." (once slashes are stripped, these are the only
// remaining path segments that could otherwise let a crafted item title
// (e.g. a scraped title of "..") make the joined download path escape the
// destination directory).
func sanitizeFilename(s string) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 32 && r != ':' && r != '<' && r != '>' && r != '|' && r != '/' && r != '\\' && r != '?' && r != '*' {
			clean = append(clean, r)
		}
	}
	result := string(clean)
	if result == "" || result == "." || result == ".." {
		return ""
	}
	return result
}

// extFromURL extracts the file extension from a URL's path component.
// Returns "" if the URL has no path or the path has no extension.
func extFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return path.Ext(u.Path)
}

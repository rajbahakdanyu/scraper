// internal/downloader/downloader.go
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
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
	readTimeout time.Duration
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
			Timeout: 0, // no total timeout — we use per-read deadline instead
		},
		concurrency: concurrency,
		userAgent:   userAgent,
		progress:    make(chan model.ProgressEvent, progressBufSize),
		cancelMap:   make(map[string]context.CancelFunc),
		sem:         make(chan struct{}, concurrency),
		readTimeout: timeout,
	}
}

// Download fetches a single media item and saves it to dest. It uses a
// temporary ".part" file during download and renames it on success.
// Retries up to maxRetries times on transient errors; if all retries are
// exhausted (or the download is cancelled) the .part file is kept rather
// than deleted, so a later Download call for the same item resumes from
// where it left off via an HTTP Range request instead of starting over.
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
	// Only append extension from URL if the title doesn't already have one
	if path.Ext(filename) == "" {
		if ext := extFromURL(item.URL); ext != "" {
			filename += ext
		}
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
			case <-time.After(backoffWithJitter(retryBaseDelay, attempt)):
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
		var nre *nonRetryableError
		if errors.As(err, &nre) {
			// Client errors (404, 403, ...) will never succeed no matter
			// how many times we retry — stop immediately.
			break
		}
	}

	if ctx.Err() != nil {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		return fmt.Errorf("download cancelled: %s", item.URL)
	}

	var nre *nonRetryableError
	if errors.As(lastErr, &nre) {
		// Will never succeed no matter how many times it's retried, so
		// there's nothing worth resuming — clean up the partial file.
		os.Remove(tempPath)
	}
	// Otherwise tempPath is left in place: a later Download call for the
	// same item resumes from it via a Range request instead of
	// re-downloading everything from scratch.
	d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
	return fmt.Errorf("download failed after %d attempts: %w", maxRetries, lastErr)
}

// downloadOnce performs a single download attempt to tempPath. The request
// and the response body read are both bound to ctx, so cancelling ctx
// aborts an in-flight attempt immediately instead of waiting for it to
// finish. If tempPath already has bytes on disk (a previous attempt left
// them there), it requests the remainder via a Range header and appends;
// if the server doesn't honor the range (a plain 200 response) it falls
// back to a full re-download instead of corrupting the file.
func (d *Downloader) downloadOnce(ctx context.Context, item model.MediaItem, tempPath string) error {
	var existing int64
	if info, err := os.Stat(tempPath); err == nil {
		existing = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
	if err != nil {
		return err
	}
	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}
	if existing > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// The server ignored our Range request (or we didn't send one).
		// Bytes already on disk can't be trusted to precede this
		// full-content response, so start over from scratch.
		existing = 0
	case http.StatusPartialContent:
		// Resuming from existing bytes, as requested.
	case http.StatusRequestedRangeNotSatisfiable:
		// Our recorded partial no longer lines up with the remote resource
		// (e.g. it changed size) — discard it so the next retry attempt
		// fetches the whole thing fresh.
		os.Remove(tempPath)
		return fmt.Errorf("HTTP %d for %s (stale partial download discarded)", resp.StatusCode, item.URL)
	default:
		err := fmt.Errorf("HTTP %d for %s", resp.StatusCode, item.URL)
		if !isRetryableStatus(resp.StatusCode) {
			return &nonRetryableError{err}
		}
		return err
	}

	total := existing + resp.ContentLength
	if resp.ContentLength < 0 {
		total = existing
	}
	if fromHeader := parseContentRangeTotal(resp.Header.Get("Content-Range")); fromHeader > 0 {
		total = fromHeader
	}

	flags := os.O_CREATE | os.O_WRONLY
	if existing > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(tempPath, flags, 0644)
	if err != nil {
		return err
	}

	pr := &progressReader{
		reader: resp.Body,
		base:   existing,
		onProgress: func(bytes int64) {
			d.sendProgress(model.ProgressEvent{
				Item:   item,
				Bytes:  bytes,
				Total:  total,
				Status: model.StatusDownloading,
			})
		},
		readDeadline: d.readTimeout,
	}

	_, err = io.Copy(out, pr)
	out.Close()
	if err != nil {
		return err
	}

	return nil
}

// progressReader wraps an io.Reader and calls onProgress after each Read
// with the cumulative number of bytes read. If no data is received within
// readDeadline, the next Read returns an error (stalled connection detection).
type progressReader struct {
	reader       io.Reader
	read         int64
	base         int64 // bytes already on disk before this read began (resumed downloads)
	onProgress   func(bytes int64)
	readDeadline time.Duration
}

func (pr *progressReader) Read(p []byte) (int, error) {
	// Use a goroutine + channel to enforce a per-read deadline
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := pr.reader.Read(p)
		done <- result{n, err}
	}()

	timeout := pr.readDeadline
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	select {
	case r := <-done:
		if r.n > 0 {
			pr.read += int64(r.n)
			pr.onProgress(pr.base + pr.read)
		}
		return r.n, r.err
	case <-time.After(timeout):
		return 0, fmt.Errorf("read timeout: no data for %s", timeout)
	}
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

// nonRetryableError marks an error that will never succeed no matter how
// many times it's retried (e.g. an HTTP 4xx client error other than 408/429).
type nonRetryableError struct{ err error }

func (e *nonRetryableError) Error() string { return e.err.Error() }
func (e *nonRetryableError) Unwrap() error { return e.err }

// backoffWithJitter returns an exponentially increasing delay (base *
// 2^attempt) with up to ~30% random jitter added, so many concurrent
// downloads retrying around the same time don't all retry at once.
func backoffWithJitter(base time.Duration, attempt int) time.Duration {
	d := base << uint(attempt)
	jitter := time.Duration(rand.Int63n(int64(d)/3 + 1))
	return d + jitter
}

// isRetryableStatus reports whether an HTTP status code represents a
// transient failure worth retrying (5xx, request timeout, rate limiting) as
// opposed to a client error (bad URL, forbidden, not found, ...) that will
// never succeed no matter how many times it's retried.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	default:
		return code >= 500
	}
}

// parseContentRangeTotal extracts the total resource size from a
// "Content-Range: bytes start-end/total" response header, returning 0 if
// the header is absent or malformed.
func parseContentRangeTotal(headerVal string) int64 {
	idx := strings.LastIndex(headerVal, "/")
	if idx < 0 || idx == len(headerVal)-1 {
		return 0
	}
	total, err := strconv.ParseInt(headerVal[idx+1:], 10, 64)
	if err != nil {
		return 0
	}
	return total
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

// internal/downloader/downloader.go
package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

const (
	maxRetries      = 3
	retryBaseDelay  = 500 * time.Millisecond
	progressBufSize = 4096
	// minRateLimitBurst is the smallest token-bucket burst SetRateLimit will
	// configure, regardless of how low the requested rate is. It just needs
	// to comfortably cover a single io.Copy read chunk so WaitN never
	// rejects a read outright for exceeding the burst size.
	minRateLimitBurst = 32 * 1024
)

// Downloader manages concurrent media downloads with progress tracking,
// cancellation, retry, and atomic file writes.
type Downloader struct {
	client        *http.Client
	userAgent     string
	progress      chan model.ProgressEvent
	cancelMap     map[string]context.CancelFunc
	queued        map[string]struct{}
	cancelled     map[string]struct{}
	mu            sync.Mutex
	sem           chan struct{}
	limiter       *rate.Limiter
	skipChecksums bool
	paths         *fileutil.PathAllocator
	// wg tracks in-flight Download calls. Shutdown closes the progress
	// channel only after wg drains, so a sendProgress can never hit a
	// closed channel. shuttingDown (guarded by mu) makes post-Shutdown
	// Download calls fail fast instead of joining wg.
	wg           sync.WaitGroup
	shuttingDown bool
}

// NewDownloaderWithConfig creates a Downloader using settings from a config.
func NewDownloaderWithConfig(concurrency int, timeout time.Duration, userAgent, proxyURL string) (*Downloader, error) {
	if concurrency < 1 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if userAgent == "" {
		userAgent = "Scraper/1.0"
	}
	var client *http.Client
	var err error
	if proxyURL != "" {
		client, err = netutil.NewPublicStreamingClientWithProxy(timeout, proxyURL)
	} else {
		client, err = netutil.NewPublicStreamingClient(timeout)
	}
	if err != nil {
		return nil, err
	}
	return &Downloader{
		client:    client,
		userAgent: userAgent,
		progress:  make(chan model.ProgressEvent, progressBufSize),
		cancelMap: make(map[string]context.CancelFunc),
		queued:    make(map[string]struct{}),
		cancelled: make(map[string]struct{}),
		sem:       make(chan struct{}, concurrency),
		paths:     fileutil.NewPathAllocator(),
	}, nil
}

func (d *Downloader) Prepare(item model.MediaItem) {
	d.mu.Lock()
	d.queued[item.Key()] = struct{}{}
	d.mu.Unlock()
}

// SetRateLimit caps the combined download speed across all concurrent
// downloads to bytesPerSec. A value <= 0 removes any cap (the default).
// Call this before starting downloads, not concurrently with them.
func (d *Downloader) SetRateLimit(bytesPerSec int64) {
	if bytesPerSec <= 0 {
		d.limiter = nil
		return
	}
	d.limiter = rate.NewLimiter(rate.Limit(bytesPerSec), minRateLimitBurst)
}

// SetSkipChecksums disables writing and verifying the .sha256 sidecar file
// that normally accompanies each completed download. Call this before
// starting downloads, not concurrently with them.
func (d *Downloader) SetSkipChecksums(skip bool) {
	d.skipChecksums = skip
}

// Download fetches a single media item and saves it to dest. It uses a
// temporary ".part" file during download and renames it on success.
// Retries up to maxRetries times on transient errors; if all retries are
// exhausted (or the download is cancelled) the .part file is kept rather
// than deleted, so a later Download call for the same item resumes from
// where it left off via an HTTP Range request instead of starting over.
//
// Items with a Resolve func get their final download URL lazily, at
// download time and bound to the download's ctx: signed URLs expire, so
// resolving at parse time would guarantee stale signatures for later items
// in a long queue. A 403 on a resolved URL (an expired signature) is
// retried once with a freshly re-resolved URL instead of failing
// permanently; a second 403 is permanent like any other client error.
func (d *Downloader) Download(item model.MediaItem, dest string) error {
	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	if d.shuttingDown {
		// Fail fast after Shutdown. This path is not tracked by d.wg, so
		// it must not sendProgress — the channel may already be closed.
		d.mu.Unlock()
		cancel()
		return fmt.Errorf("downloader is shut down: %s", item.URL)
	}
	d.wg.Add(1)
	delete(d.queued, item.Key())
	if _, ok := d.cancelled[item.Key()]; ok {
		delete(d.cancelled, item.Key())
		d.mu.Unlock()
		cancel()
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		d.wg.Done()
		return fmt.Errorf("download cancelled: %s", item.URL)
	}
	d.cancelMap[item.Key()] = cancel
	d.mu.Unlock()
	defer d.wg.Done()
	defer func() {
		d.mu.Lock()
		delete(d.cancelMap, item.Key())
		d.mu.Unlock()
		cancel()
	}()

	select {
	case d.sem <- struct{}{}:
		defer func() { <-d.sem }()
	case <-ctx.Done():
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		return fmt.Errorf("download cancelled: %s", item.URL)
	}

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
	finalPath := d.paths.Claim(filepath.Join(dest, filename), item.Key())
	tempPath := finalPath + ".part"
	resumePath := resumeMetadataPath(tempPath)

	// Skip if the file already exists, is non-empty, and (when a checksum
	// sidecar is present) still matches it.
	if info, err := os.Stat(finalPath); err == nil && info.Size() > 0 {
		if d.skipChecksums {
			d.sendProgress(model.ProgressEvent{
				Item:     item,
				Status:   model.StatusCompleted,
				Filename: finalPath,
			})
			return nil
		}
		present, matches, verifyErr := fileutil.VerifyChecksum(finalPath)
		if verifyErr != nil {
			d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
			return fmt.Errorf("verify existing checksum for %s: %w", finalPath, verifyErr)
		}
		if !present || matches {
			d.sendProgress(model.ProgressEvent{
				Item:     item,
				Status:   model.StatusCompleted,
				Filename: finalPath,
			})
			return nil
		}
		// Existing file no longer matches its checksum — treat it as
		// corrupt and redownload rather than trusting stale bytes.
		if err := os.Remove(finalPath); err != nil {
			return fmt.Errorf("remove corrupt file %s: %w", finalPath, err)
		}
		if err := os.Remove(fileutil.ChecksumPath(finalPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale checksum for %s: %w", finalPath, err)
		}
	}

	d.sendProgress(model.ProgressEvent{
		Item:   item,
		Status: model.StatusDownloading,
		Start:  time.Now(),
	})

	// resolvedURL is the URL actually fetched. For items with a Resolve
	// func it starts empty and is produced lazily inside the retry loop,
	// so a transient resolution failure is retried with backoff like any
	// other transient failure, and a 403 can trigger one re-resolve.
	resolvedURL := item.URL
	if item.Resolve != nil {
		resolvedURL = ""
	}
	reResolved := false

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

		if item.Resolve != nil && resolvedURL == "" {
			u, err := item.Resolve(ctx)
			if err != nil {
				lastErr = err
				if ctx.Err() != nil {
					break
				}
				var pe *model.PermanentError
				if errors.As(err, &pe) {
					// The resolver asserts this can never succeed
					// (e.g. a 404 from the resolve API): fail
					// permanently, same as a nonRetryable HTTP status.
					lastErr = &nonRetryableError{err: err}
					break
				}
				continue
			}
			resolvedURL = u
		}

		err := d.downloadOnce(ctx, item, resolvedURL, tempPath)
		if err == nil {
			if err := os.Rename(tempPath, finalPath); err != nil {
				return errors.Join(err, removeFiles(tempPath, resumePath))
			}
			if err := removeFiles(resumePath); err != nil {
				return err
			}
			if !d.skipChecksums {
				if err := fileutil.WriteChecksum(finalPath); err != nil {
					d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
					return fmt.Errorf("write checksum for %s: %w", finalPath, err)
				}
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
			// A 403 on a lazily resolved signed URL almost always means
			// the signature expired between resolution and request —
			// re-resolve once for a fresh URL and retry with it. A
			// second 403 (or any nonRetryable failure for an item
			// without a resolver) stays permanent.
			if item.Resolve != nil && !reResolved && nre.statusCode == http.StatusForbidden {
				reResolved = true
				resolvedURL = ""
				continue
			}
			// Client errors (404, 403, ...) will never succeed no matter
			// how many times we retry — stop immediately.
			break
		}
	}

	if ctx.Err() != nil {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		return fmt.Errorf("download cancelled: %s", item.URL)
	}

	var cleanupErr error
	var nre *nonRetryableError
	if errors.As(lastErr, &nre) {
		// Will never succeed no matter how many times it's retried, so
		// there's nothing worth resuming — clean up the partial file.
		cleanupErr = removeFiles(tempPath, resumePath)
	}
	// Otherwise tempPath is left in place: a later Download call for the
	// same item resumes from it via a Range request instead of
	// re-downloading everything from scratch.
	d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
	return fmt.Errorf("download failed after %d attempts: %w", maxRetries, errors.Join(lastErr, cleanupErr))
}

// downloadOnce performs a single download attempt to tempPath, fetching
// resolvedURL (the item's final download URL, produced by its Resolve func
// when it has one). The request and the response body read are both bound
// to ctx, so cancelling ctx aborts an in-flight attempt immediately
// instead of waiting for it to finish. If tempPath already has bytes on
// disk (a previous attempt left them there), it requests the remainder via
// a Range header and appends; if the server doesn't honor the range (a
// plain 200 response) it falls back to a full re-download instead of
// corrupting the file.
func (d *Downloader) downloadOnce(ctx context.Context, item model.MediaItem, resolvedURL, tempPath string) error {
	var existing int64
	var resume resumeMetadata
	if info, err := os.Stat(tempPath); err == nil {
		existing = info.Size()
	}
	if existing > 0 {
		metadata, err := readResumeMetadata(tempPath)
		// Items with a Resolve func get a freshly signed URL every run, so
		// the recorded URL legitimately differs from this run's resolved
		// URL. What actually protects resume integrity is the validator
		// (ETag/Last-Modified, sent as If-Range and re-checked against the
		// response) — URL equality is only an extra guard for items whose
		// URL is final and stable.
		urlMismatch := item.Resolve == nil && metadata.URL != resolvedURL
		if err != nil || metadata.validator() == "" || urlMismatch {
			existing = 0
		} else {
			resume = metadata
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", resolvedURL, nil)
	if err != nil {
		return err
	}
	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}
	if existing > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
		req.Header.Set("If-Range", resume.validator())
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
		start, _, _, err := parseContentRange(resp.Header.Get("Content-Range"))
		if existing == 0 || err != nil || start != existing || !resume.matches(resp) {
			cleanupErr := removeFiles(tempPath, resumeMetadataPath(tempPath))
			return errors.Join(fmt.Errorf("invalid partial response for %s; discarded stale partial", resolvedURL), cleanupErr)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// A 416 whose Content-Range total equals our on-disk byte count
		// means the partial already IS the complete resource (a previous
		// run finished the body but died before the rename). Keep it and
		// let Download rename it into place instead of re-downloading
		// everything. The validator must still match, so a resource that
		// changed to the same size doesn't get trusted by accident.
		if existing > 0 && resume.matches(resp) {
			if total, err := parseContentRangeTotal(resp.Header.Get("Content-Range")); err == nil && total == existing {
				return nil
			}
		}
		// Our recorded partial no longer lines up with the remote resource
		// (e.g. it changed size) — discard it so the next retry attempt
		// fetches the whole thing fresh.
		cleanupErr := removeFiles(tempPath, resumeMetadataPath(tempPath))
		return errors.Join(fmt.Errorf("HTTP %d for %s (stale partial download discarded)", resp.StatusCode, resolvedURL), cleanupErr)
	default:
		err := fmt.Errorf("HTTP %d for %s", resp.StatusCode, resolvedURL)
		if !isRetryableStatus(resp.StatusCode) {
			return &nonRetryableError{err: err, statusCode: resp.StatusCode}
		}
		return err
	}

	total := existing + resp.ContentLength
	if resp.ContentLength < 0 {
		total = existing
	}
	if _, _, fromHeader, err := parseContentRange(resp.Header.Get("Content-Range")); err == nil {
		total = fromHeader
	}
	// The response carried no length of its own (chunked/HTTP2): fall back
	// to the scraper-reported item size so a silently truncated body is
	// still caught by the size check below. Skip this when the transport
	// transparently decompressed the body — decompressed bytes legitimately
	// differ from the on-the-wire size the scraper reported.
	if total <= 0 && item.Size > 0 && !resp.Uncompressed {
		total = item.Size
	}

	flags := os.O_CREATE | os.O_WRONLY
	if existing > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	// #nosec G304 -- tempPath is allocated beneath the configured output directory from a sanitized title.
	out, err := os.OpenFile(tempPath, flags, 0600)
	if err != nil {
		return err
	}
	if existing == 0 {
		resume = metadataFromResponse(resolvedURL, resp)
		if resume.validator() == "" {
			if err := removeFiles(resumeMetadataPath(tempPath)); err != nil {
				return errors.Join(err, out.Close())
			}
		} else if err := writeResumeMetadata(tempPath, resume); err != nil {
			return errors.Join(err, out.Close())
		}
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
		limiter: d.limiter,
		ctx:     ctx,
	}

	_, copyErr := io.Copy(out, pr)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if total > 0 {
		info, statErr := os.Stat(tempPath)
		if statErr != nil {
			return statErr
		}
		if info.Size() != total {
			return fmt.Errorf("download size mismatch for %s: got %d, want %d", resolvedURL, info.Size(), total)
		}
	}

	return nil
}

// progressReader wraps an io.Reader and calls onProgress after each Read.
// Network idle deadlines are enforced by netutil's streaming transport.
type progressReader struct {
	reader     io.Reader
	read       int64
	base       int64 // bytes already on disk before this read began (resumed downloads)
	onProgress func(bytes int64)
	limiter    *rate.Limiter
	ctx        context.Context
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		if pr.limiter != nil {
			if waitErr := pr.limiter.WaitN(pr.ctx, n); waitErr != nil {
				return n, waitErr
			}
		}
		pr.read += int64(n)
		pr.onProgress(pr.base + pr.read)
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
	if cancel, ok := d.cancelMap[item.Key()]; ok {
		cancel()
		delete(d.cancelMap, item.Key())
	} else if _, ok := d.queued[item.Key()]; ok {
		d.cancelled[item.Key()] = struct{}{}
	}
}

// Shutdown cancels every active download — aborting in-flight HTTP
// requests and unblocking downloads still waiting on the concurrency
// semaphore — and closes the progress channel once the last in-flight
// Download returns, so a consumer ranging over Progress() terminates.
// sendProgress is only ever called from Download goroutines (tracked by
// d.wg), so closing after they all return can never panic with a send on
// a closed channel. Shutdown is idempotent and non-blocking (the channel
// closes asynchronously); afterwards Download calls fail fast instead of
// starting new work.
func (d *Downloader) Shutdown() {
	d.mu.Lock()
	if d.shuttingDown {
		d.mu.Unlock()
		return
	}
	d.shuttingDown = true
	cancels := make([]context.CancelFunc, 0, len(d.cancelMap))
	for _, cancel := range d.cancelMap {
		cancels = append(cancels, cancel)
	}
	d.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	go func() {
		d.wg.Wait()
		close(d.progress)
	}()
}

// Progress returns a read-only channel of download progress events. The
// channel is closed by Shutdown once all in-flight downloads have returned.
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
// many times it's retried (e.g. an HTTP 4xx client error other than
// 408/429). statusCode is the HTTP status that caused it, or 0 when the
// failure wasn't an HTTP response (e.g. a permanent resolver error).
type nonRetryableError struct {
	err        error
	statusCode int
}

func (e *nonRetryableError) Error() string { return e.err.Error() }
func (e *nonRetryableError) Unwrap() error { return e.err }

// backoffWithJitter returns an exponentially increasing delay (base *
// 2^attempt) with up to ~30% random jitter added, so many concurrent
// downloads retrying around the same time don't all retry at once.
func backoffWithJitter(base time.Duration, attempt int) time.Duration {
	d := base << uint(attempt)
	// #nosec G404 -- retry jitter does not require cryptographic randomness.
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

func parseContentRange(headerVal string) (start, end, total int64, err error) {
	if _, scanErr := fmt.Sscanf(headerVal, "bytes %d-%d/%d", &start, &end, &total); scanErr != nil {
		return 0, 0, 0, scanErr
	}
	if start < 0 || end < start || total <= end {
		return 0, 0, 0, fmt.Errorf("invalid Content-Range %q", headerVal)
	}
	return start, end, total, nil
}

// parseContentRangeTotal parses the "bytes */<total>" form of Content-Range
// that accompanies a 416 response, returning just the resource's total size.
func parseContentRangeTotal(headerVal string) (int64, error) {
	var total int64
	if _, err := fmt.Sscanf(headerVal, "bytes */%d", &total); err != nil {
		return 0, err
	}
	if total < 0 {
		return 0, fmt.Errorf("invalid Content-Range %q", headerVal)
	}
	return total, nil
}

type resumeMetadata struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

func metadataFromResponse(rawURL string, resp *http.Response) resumeMetadata {
	etag := resp.Header.Get("ETag")
	if strings.HasPrefix(etag, "W/") {
		etag = ""
	}
	return resumeMetadata{
		URL:          rawURL,
		ETag:         etag,
		LastModified: resp.Header.Get("Last-Modified"),
	}
}

func (m resumeMetadata) validator() string {
	if m.ETag != "" {
		return m.ETag
	}
	return m.LastModified
}

func (m resumeMetadata) matches(resp *http.Response) bool {
	if m.ETag != "" {
		return resp.Header.Get("ETag") == "" || resp.Header.Get("ETag") == m.ETag
	}
	return resp.Header.Get("Last-Modified") == "" || resp.Header.Get("Last-Modified") == m.LastModified
}

func resumeMetadataPath(tempPath string) string {
	return tempPath + ".resume.json"
}

func readResumeMetadata(tempPath string) (resumeMetadata, error) {
	data, err := os.ReadFile(resumeMetadataPath(tempPath))
	if err != nil {
		return resumeMetadata{}, err
	}
	var metadata resumeMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return resumeMetadata{}, err
	}
	return metadata, nil
}

func writeResumeMetadata(tempPath string, metadata resumeMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return os.WriteFile(resumeMetadataPath(tempPath), data, 0600)
}

func removeFiles(paths ...string) error {
	var errs []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
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

// internal/downloader/downloader.go
package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/downloadercore"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

// MaxUnknownFileSize bounds downloads whose response does not provide a
// trustworthy total size. Tests may temporarily reduce this value.
var MaxUnknownFileSize int64 = 64 << 30

// renameFile is a test seam for the final atomic rename.
var renameFile = os.Rename

// Downloader manages concurrent media downloads with progress tracking,
// cancellation, retry, and atomic file writes.
type Downloader struct {
	*downloadercore.Core
	client    *http.Client
	userAgent string
	// These aliases keep existing package tests able to inspect coordination
	// state while the embedded core owns the state and its lifecycle.
	mu            *sync.Mutex
	cancelMap     map[string]context.CancelFunc
	sem           chan struct{}
	skipChecksums bool
	paths         *fileutil.PathAllocator
	sleeper       netutil.Sleeper
}

// NewDownloaderWithConfig creates a Downloader using settings from a config.
func NewDownloaderWithConfig(concurrency int, timeout time.Duration, userAgent, proxyURL string, cookies []*http.Cookie) (*Downloader, error) {
	if concurrency < 1 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if userAgent == "" {
		userAgent = config.DefaultUserAgent
	}
	var client *http.Client
	var err error
	if proxyURL != "" {
		client, err = netutil.NewPublicStreamingClientWithProxyAndConcurrency(concurrency, timeout, proxyURL)
	} else {
		client, err = netutil.NewPublicStreamingClientWithConcurrency(concurrency, timeout)
	}
	if err != nil {
		return nil, err
	}
	client = netutil.WithCookies(client, cookies)
	core := downloadercore.New(concurrency, downloadercore.ProgressBufSize, nil, false)
	return &Downloader{
		Core:      core,
		client:    client,
		userAgent: userAgent,
		mu:        core.Mutex(),
		cancelMap: core.CancelMap(),
		sem:       core.Semaphore(),
		paths:     fileutil.NewPathAllocator(),
		sleeper:   netutil.RealSleeper{},
	}, nil
}

// SetSleeper replaces the delay implementation. Call this before starting
// downloads, not concurrently with them.
func (d *Downloader) SetSleeper(s netutil.Sleeper) { d.sleeper = s }

func (d *Downloader) Prepare(item model.MediaItem) {
	d.Core.Prepare(item.Key())
}

// SetRateLimit caps the combined download speed across all concurrent
// downloads to bytesPerSec. A value <= 0 removes any cap (the default).
// Call this before starting downloads, not concurrently with them.
func (d *Downloader) SetRateLimit(bytesPerSec int64) {
	if bytesPerSec <= 0 {
		d.Core.SetLimiter(nil)
		return
	}
	d.Core.SetLimiter(rate.NewLimiter(rate.Limit(bytesPerSec), downloadercore.MinRateLimitBurst))
}

// SetSkipChecksums disables writing and verifying the .sha256 sidecar file
// that normally accompanies each completed download. Call this before
// starting downloads, not concurrently with them.
func (d *Downloader) SetSkipChecksums(skip bool) {
	d.skipChecksums = skip
}

// SkipChecksums reports whether checksum sidecar writing/verification is
// disabled for this downloader.
func (d *Downloader) SkipChecksums() bool { return d.skipChecksums }

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
func (d *Downloader) Download(item model.MediaItem, dest string) (string, error) {
	ctx, _, release, err := d.Core.Begin(item.Key(), item, model.ProgressEvent{Item: item, Status: model.StatusCancelled})
	if err != nil {
		return "", err
	}
	defer release()

	filename := fileutil.SanitizeFilename(item.Title)
	if filename == "" {
		filename = "download"
	}
	// Only append extension from URL if the title doesn't already have one.
	// Re-sanitize the combined name so the byte-budget clamp accounts for
	// the extension.
	if path.Ext(filename) == "" {
		if ext := extFromURL(item.URL); ext != "" {
			filename = fileutil.SanitizeFilename(filename + ext)
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
			return finalPath, nil
		}
		present, matches, verifyErr := fileutil.VerifyChecksum(finalPath)
		if verifyErr != nil {
			d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
			return "", fmt.Errorf("verify existing checksum for %s: %w", finalPath, verifyErr)
		}
		if !present || matches {
			d.sendProgress(model.ProgressEvent{
				Item:     item,
				Status:   model.StatusCompleted,
				Filename: finalPath,
			})
			return finalPath, nil
		}
		// Existing file no longer matches its checksum — treat it as
		// corrupt and redownload rather than trusting stale bytes.
		if err := os.Remove(finalPath); err != nil {
			return "", fmt.Errorf("remove corrupt file %s: %w", finalPath, err)
		}
		if err := os.Remove(fileutil.ChecksumPath(finalPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("remove stale checksum for %s: %w", finalPath, err)
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
	var checksum string
	lastErr = netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, d.sleeper, func() error {
		if item.Resolve != nil && resolvedURL == "" {
			u, err := item.Resolve(ctx)
			if err != nil {
				var pe *model.PermanentError
				if errors.As(err, &pe) {
					return model.NewNonRetryable(err)
				}
				return err
			}
			resolvedURL = u
		}
		attemptChecksum, err := d.downloadOnce(ctx, item, resolvedURL, tempPath)
		if err == nil {
			checksum = attemptChecksum
			return nil
		}
		var nre *model.NonRetryableError
		if item.Resolve != nil && !reResolved && shouldRefreshURL(err) {
			reResolved = true
			resolvedURL = ""
			if errors.As(err, &nre) {
				// A 403 is non-retryable on its own; %v (not %w) keeps
				// netutil.Retry from stopping before the refreshed URL
				// is tried.
				return fmt.Errorf("retry with refreshed URL: %v", err)
			}
			return fmt.Errorf("retry with refreshed URL: %w", err)
		}
		return err
	})
	if lastErr == nil {
		if err := renameFile(tempPath, finalPath); err != nil {
			d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
			return "", errors.Join(err, removeFiles(tempPath, resumePath))
		}
		if !d.skipChecksums {
			if err := fileutil.WriteChecksumDigest(finalPath, checksum); err != nil {
				d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
				return "", fmt.Errorf("write checksum for %s: %w", finalPath, err)
			}
		}
		_ = removeFiles(resumePath)
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCompleted, Filename: finalPath})
		return finalPath, nil
	}

	if ctx.Err() != nil {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		return "", &model.CancelledError{URL: item.URL}
	}

	var cleanupErr error
	var nre *model.NonRetryableError
	if errors.As(lastErr, &nre) {
		// Will never succeed no matter how many times it's retried, so
		// there's nothing worth resuming — clean up the partial file.
		cleanupErr = removeFiles(tempPath, resumePath)
	}
	// Otherwise tempPath is left in place: a later Download call for the
	// same item resumes from it via a Range request instead of
	// re-downloading everything from scratch.
	d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
	return "", fmt.Errorf("download failed after %d attempts: %w", netutil.MaxRetries, errors.Join(lastErr, cleanupErr))
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
func (d *Downloader) downloadOnce(ctx context.Context, item model.MediaItem, resolvedURL, tempPath string) (string, error) {
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
		return "", err
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
		return "", redactConnectionError(err, resolvedURL)
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
			return "", errors.Join(fmt.Errorf("invalid partial response for %s; discarded stale partial", redactURL(resolvedURL)), cleanupErr)
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
				return hashFileDigest(tempPath)
			}
		}
		// Our recorded partial no longer lines up with the remote resource
		// (e.g. it changed size) — discard it so the next retry attempt
		// fetches the whole thing fresh.
		cleanupErr := removeFiles(tempPath, resumeMetadataPath(tempPath))
		return "", errors.Join(fmt.Errorf("HTTP %d for %s (stale partial download discarded)", resp.StatusCode, redactURL(resolvedURL)), cleanupErr)
	default:
		err := fmt.Errorf("HTTP %d for %s", resp.StatusCode, redactURL(resolvedURL))
		if !model.IsRetryableStatus(resp.StatusCode) {
			return "", model.NewNonRetryableStatus(err, resp.StatusCode)
		}
		return "", model.NewHTTPStatusError(
			err,
			resp.StatusCode,
			redactURL(resolvedURL),
			parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		)
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
	out, err := fileutil.OpenFileNoFollow(tempPath, flags, 0644)
	if err != nil {
		if fileutil.IsSymlinkError(err) {
			return "", fmt.Errorf("refusing to follow symlink for .part file %s: %w", tempPath, err)
		}
		return "", err
	}
	if existing == 0 {
		resume = metadataFromResponse(resolvedURL, resp, item.Resolve == nil)
		if resume.validator() == "" {
			if err := removeFiles(resumeMetadataPath(tempPath)); err != nil {
				return "", errors.Join(err, out.Close())
			}
		} else if err := writeResumeMetadata(tempPath, resume); err != nil {
			return "", errors.Join(err, out.Close())
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
		limiter: d.Core.Limiter(),
		ctx:     ctx,
	}

	hasher := sha256.New()
	if existing > 0 {
		if err := seedHasher(tempPath, hasher); err != nil {
			return "", errors.Join(err, out.Close())
		}
	}
	tee := io.TeeReader(pr, hasher)
	var source io.Reader = tee
	var unknownSizeLimit *io.LimitedReader
	unknownSize := total <= 0 || (existing > 0 && resp.ContentLength < 0)
	if unknownSize {
		unknownSizeLimit = &io.LimitedReader{R: tee, N: MaxUnknownFileSize + 1}
		source = unknownSizeLimit
	}
	copiedBytes, copyErr := io.CopyBuffer(out, source, make([]byte, 512*1024))
	closeErr := out.Close()
	if unknownSizeLimit != nil && copiedBytes > MaxUnknownFileSize {
		err := model.NewNonRetryable(fmt.Errorf("download exceeds maximum unknown file size of %d bytes", MaxUnknownFileSize))
		return "", errors.Join(err, closeErr)
	}
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if total > 0 {
		info, statErr := os.Stat(tempPath)
		if statErr != nil {
			return "", statErr
		}
		if info.Size() != total {
			return "", fmt.Errorf("download size mismatch for %s: got %d, want %d", redactURL(resolvedURL), info.Size(), total)
		}
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func seedHasher(path string, hasher io.Writer) error {
	// #nosec G304 -- tempPath is allocated beneath the configured output directory.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(hasher, file)
	return err
}

func hashFileDigest(path string) (string, error) {
	hasher := sha256.New()
	if err := seedHasher(path, hasher); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
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
				return 0, waitErr
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
	d.Core.Cancel(item.Key())
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
	d.Core.Shutdown()
}

// Progress returns a read-only channel of download progress events. The
// channel is closed by Shutdown once all in-flight downloads have returned.
func (d *Downloader) Progress() <-chan model.ProgressEvent {
	return d.Core.Progress()
}

// sendProgress sends an event. Uses a large buffer (4096) so events
// are rarely dropped. If the buffer is full the event is dropped to
// prevent stalling the download goroutine.
func (d *Downloader) sendProgress(evt model.ProgressEvent) {
	d.Core.SendProgress(evt)
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

func metadataFromResponse(rawURL string, resp *http.Response, storeURL bool) resumeMetadata {
	etag := resp.Header.Get("ETag")
	if strings.HasPrefix(etag, "W/") {
		etag = ""
	}
	url := ""
	if storeURL {
		url = rawURL
	}
	return resumeMetadata{
		URL:          url,
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
	target := resumeMetadataPath(tempPath)
	// Write to a temp file and rename into place: rename replaces a symlink
	// instead of writing through it, so a planted link cannot redirect the
	// write at an arbitrary victim file (same defense as the checksum sidecar).
	temp, err := os.CreateTemp(filepath.Dir(target), ".resume-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0600); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.Write(data); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, target)
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

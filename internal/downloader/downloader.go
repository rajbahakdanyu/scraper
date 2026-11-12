// internal/downloader/downloader.go
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
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
	client        *http.Client
	userAgent     string
	skipChecksums bool
	paths         *fileutil.PathAllocator
	sleeper       netutil.Sleeper
	hosts         *hostGate
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
	return &Downloader{
		Core:      downloadercore.New(concurrency, downloadercore.ProgressBufSize, nil, false),
		client:    client,
		userAgent: userAgent,
		paths:     fileutil.NewPathAllocator(),
		sleeper:   netutil.RealSleeper{},
		hosts:     newHostGate(0),
	}, nil
}

// SetSleeper replaces the delay implementation. Call this before starting
// downloads, not concurrently with them.
func (d *Downloader) SetSleeper(s netutil.Sleeper) { d.sleeper = s }

func (d *Downloader) Prepare(item model.MediaItem) {
	d.Core.Prepare(item.Key())
}

// SetPerHostLimit caps simultaneous requests to any one host (n <= 0
// removes the cap; the default). Call this before starting downloads, not
// concurrently with them.
func (d *Downloader) SetPerHostLimit(n int) {
	d.hosts = newHostGate(n)
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

	if err := os.MkdirAll(dest, 0750); err != nil {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
		return "", model.NewNonRetryable(fmt.Errorf("create output directory: %w", err))
	}

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
	identity := itemIdentity(item)
	finalPath, err := d.paths.ClaimAvailable(filepath.Join(dest, filename), item.Key(), func(path string) (bool, error) {
		info, statErr := os.Lstat(path)
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return false, nil
			}
			return fileutil.VerifySource(path, identity, item.Size)
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return false, statErr
		}
		partInfo, partErr := os.Lstat(path + ".part")
		if errors.Is(partErr, os.ErrNotExist) {
			return true, nil
		}
		if partErr != nil {
			return false, partErr
		}
		if !partInfo.Mode().IsRegular() {
			if partInfo.Mode()&os.ModeSymlink != 0 {
				return false, fmt.Errorf("refusing to follow symlink %s", path+".part")
			}
			return false, nil
		}
		return resumablePartial(path, item, identity), nil
	})
	if err != nil {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
		return "", fmt.Errorf("select output path: %w", err)
	}
	tempPath := finalPath + ".part"
	resumePath := resumeMetadataPath(tempPath)

	if info, statErr := os.Lstat(finalPath); statErr == nil && info.Mode().IsRegular() {
		matches, verifyErr := fileutil.VerifySource(finalPath, identity, item.Size)
		if verifyErr != nil {
			d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
			return "", fmt.Errorf("verify existing file %s: %w", finalPath, verifyErr)
		}
		if matches {
			d.sendProgress(model.ProgressEvent{
				Item:     item,
				Status:   model.StatusCompleted,
				Filename: finalPath,
			})
			return finalPath, nil
		}
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
		return "", fmt.Errorf("inspect existing file %s: %w", finalPath, statErr)
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
	var waivedCooldown time.Time

	var lastErr error
	var checksum string
	attempts := 0
	lastErr = netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, d.sleeper, func() error {
		attempts++
		if item.Resolve != nil && resolvedURL == "" {
			u, err := item.Resolve(ctx)
			if err != nil {
				return err
			}
			resolvedURL = u
		}
		host := hostKey(resolvedURL)
		releaseHost, err := d.hosts.acquire(ctx, host, waivedCooldown, d.sleeper)
		if err != nil {
			return err
		}
		attemptChecksum, err := d.downloadOnce(ctx, item, resolvedURL, tempPath)
		releaseHost()
		var statusErr *model.HTTPStatusError
		if errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode == http.StatusServiceUnavailable) {
			waivedCooldown = d.hosts.penalize(host, max(statusErr.Delay, netutil.BackoffWithJitter(netutil.RetryBaseDelay, attempts-1)))
		}
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
		if err := fileutil.WriteSource(finalPath, identity, checksum); err != nil {
			d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
			return "", fmt.Errorf("write source metadata for %s: %w", finalPath, err)
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
	return "", fmt.Errorf("download failed after %d attempt(s): %w", attempts, errors.Join(lastErr, cleanupErr))
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
		if waitErr := downloadercore.WaitN(pr.ctx, pr.limiter, n); waitErr != nil {
			// The n bytes are already consumed from the body; hand them on
			// so the partial file stays contiguous for a later resume.
			return n, waitErr
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

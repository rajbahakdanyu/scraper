package youtube

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	configpkg "github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/downloadercore"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
	"golang.org/x/time/rate"
)

const defaultReadTimeout = 30 * time.Second

type DownloaderConfig struct {
	Concurrency  int
	Timeout      time.Duration
	UserAgent    string
	RateLimit    int64
	SkipChecksum bool
	ProxyURL     string
	Cookies      []*http.Cookie
	Quality      string // "" or "1080p" etc.
	AudioOnly    string // "" or "m4a"/"mp3"
	// Backend is BackendAuto (""), BackendBuiltin, or BackendYTDLP.
	Backend string
	// Warn receives non-fatal notices such as a quality fallback.
	Warn func(string)
}

// Downloader adapts Scraper to the dashboard's Downloader interface (see
// internal/ui/dashboard), so YouTube videos — whether from a single URL or
// enumerated from a playlist — flow through the same batch download UI as
// every other site. Each Download call resolves and fetches the
// highest-quality format available for that item, muxing video+audio with
// ffmpeg when they aren't already combined in one stream.
type Downloader struct {
	*downloadercore.Core
	scraper      *Scraper
	paths        *fileutil.PathAllocator
	quality      int    // maxHeight; 0 = no cap
	qualityLabel string // the configured cap as given, e.g. "1080p"
	audioOnly    string // "" or "m4a"/"mp3"
	sleeper      netutil.Sleeper
	warn         func(string)

	// yt-dlp backend settings.
	backend   string
	proxyURL  string
	rateLimit int64
	cookies   []*http.Cookie
}

func NewDownloaderWithConfig(scraper *Scraper, config DownloaderConfig) (*Downloader, error) {
	if config.Concurrency < 1 {
		config.Concurrency = 1
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultReadTimeout
	}
	if config.UserAgent == "" {
		config.UserAgent = configpkg.DefaultUserAgent
	}
	scraper.warn = config.Warn
	httpClient, err := netutil.NewPublicStreamingClientWithProxy(config.Timeout, config.ProxyURL)
	if err != nil {
		return nil, err
	}
	scraper.video.HTTPClient = netutil.WithCookies(netutil.WithUserAgent(httpClient, config.UserAgent), config.Cookies)

	var limiter *rate.Limiter
	if config.RateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(config.RateLimit), downloadercore.MinRateLimitBurst)
	}
	return &Downloader{
		Core:         downloadercore.New(config.Concurrency, downloadercore.ProgressBufSize, limiter, config.SkipChecksum),
		scraper:      scraper,
		paths:        fileutil.NewPathAllocator(),
		quality:      qualityHeights[config.Quality],
		qualityLabel: config.Quality,
		audioOnly:    config.AudioOnly,
		sleeper:      netutil.RealSleeper{},
		warn:         config.Warn,
		backend:      config.Backend,
		proxyURL:     config.ProxyURL,
		rateLimit:    config.RateLimit,
		cookies:      config.Cookies,
	}, nil
}

// Compile-time check that Downloader satisfies the dashboard's Downloader
// interface shape (declared inline rather than importing
// internal/ui/dashboard, which would invert the dependency direction).
var _ interface {
	Prepare(item model.MediaItem)
	Download(item model.MediaItem, dest string) (string, error)
	Cancel(item model.MediaItem)
	Shutdown()
	Progress() <-chan model.ProgressEvent
} = (*Downloader)(nil)

func (d *Downloader) Prepare(item model.MediaItem) {
	d.Core.Prepare(item.Key())
}

func (d *Downloader) Download(item model.MediaItem, dest string) (string, error) {
	ctx, _, release, err := d.Core.Begin(item.Key(), item, d.event(item, model.StatusCancelled))
	if err != nil {
		return "", err
	}
	defer release()

	if err := os.MkdirAll(dest, 0750); err != nil {
		d.sendProgress(d.event(item, model.StatusFailed))
		return "", model.NewNonRetryable(fmt.Errorf("create output directory: %w", err))
	}

	started := d.event(item, model.StatusDownloading)
	started.Start = time.Now()
	d.sendProgress(started)

	videoID := ExtractVideoID(item.URL)
	if videoID == "" {
		d.sendProgress(d.event(item, model.StatusFailed))
		return "", fmt.Errorf("youtube: could not extract video ID from %s", item.URL)
	}

	onProgress := func(bytes, total int64) {
		evt := d.event(item, model.StatusDownloading)
		evt.Bytes, evt.Total = bytes, total
		d.sendProgress(evt)
	}
	finalPath, err := d.download(ctx, item, videoID, dest, onProgress)

	switch {
	case err == nil:
		done := d.event(item, model.StatusCompleted)
		done.Filename = finalPath
		d.sendProgress(done)
		return finalPath, nil
	case ctx.Err() != nil:
		d.sendProgress(d.event(item, model.StatusCancelled))
		return "", &model.CancelledError{URL: item.URL}
	default:
		d.sendProgress(d.event(item, model.StatusFailed))
		return "", err
	}
}

func (d *Downloader) download(ctx context.Context, item model.MediaItem, videoID, dest string, onProgress func(int64, int64)) (string, error) {
	if d.backend == BackendYTDLP {
		return d.downloadWithYTDLP(ctx, item, videoID, dest, onProgress)
	}
	var finalPath string
	// Same retry policy as the generic downloader: a transient network
	// blip must not fail a whole video.
	err := netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, d.sleeper, func() error {
		var err error
		finalPath, err = d.scraper.downloadBest(ctx, videoID, dest, item.Key(), d.paths, d.Core.Limiter(), d.Core.SkipSum(), d.quality, d.audioOnly, onProgress)
		return err
	})
	if err == nil || ctx.Err() != nil || d.backend == BackendBuiltin || !fallbackWorthy(err) {
		return finalPath, err
	}
	if _, lookErr := exec.LookPath(ytDLPBinary); lookErr != nil {
		return finalPath, err
	}
	if d.warn != nil {
		d.warn(fmt.Sprintf("youtube: %s: built-in downloader failed (%v); retrying with yt-dlp", item.Title, err))
	}
	path, ytErr := d.downloadWithYTDLP(ctx, item, videoID, dest, onProgress)
	if ytErr != nil && ctx.Err() == nil {
		return path, fmt.Errorf("%w (yt-dlp fallback: %w)", err, ytErr)
	}
	return path, ytErr
}

// event builds a progress event tagged with this run's quality settings.
func (d *Downloader) event(item model.MediaItem, status model.DownloadStatus) model.ProgressEvent {
	return model.ProgressEvent{Item: item, Status: status, Quality: d.qualityLabel, AudioOnly: d.audioOnly}
}

// Cancel cancels an active download for the given item, aborting an
// in-flight stream fetch or ffmpeg mux right away rather than waiting for
// it to finish.
func (d *Downloader) Cancel(item model.MediaItem) {
	d.Core.Cancel(item.Key())
}

// Shutdown cancels every active download — aborting in-flight stream
// fetches, ffmpeg muxes, and downloads still waiting on the concurrency
// semaphore — and closes the progress channel once the last in-flight
// Download returns, so a consumer ranging over Progress() terminates.
// Shutdown is idempotent and non-blocking (the channel closes
// asynchronously); afterwards Download calls fail fast instead of
// starting new work.
func (d *Downloader) Shutdown() {
	d.Core.Shutdown()
}

// Progress returns a read-only channel of download progress events. The
// channel is closed by Shutdown once all in-flight downloads have returned.
func (d *Downloader) Progress() <-chan model.ProgressEvent {
	return d.Core.Progress()
}

func (d *Downloader) sendProgress(evt model.ProgressEvent) {
	d.Core.SendProgress(evt)
}

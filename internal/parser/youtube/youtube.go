// internal/parser/youtube/youtube.go
package youtube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
	"golang.org/x/time/rate"
)

type Scraper struct {
	video *ytdl.Client
}

// NewYouTubeScraper creates a YouTube scraper using the kkdai/youtube library
// for real stream URL extraction and downloads.
func NewYouTubeScraper() *Scraper {
	return &Scraper{
		video: &ytdl.Client{HTTPClient: netutil.NewPublicClient(defaultReadTimeout)},
	}
}

func (s *Scraper) Name() string {
	return "youtube"
}

// Parse returns one MediaItem per video: a single item for a plain video
// URL, or one item per video in the playlist if the URL has a "list" query
// parameter. Quality is resolved lazily at download time (see
// selectBestFormats), not here, so parsing a large playlist doesn't have
// to pre-resolve the formats of every video up front.
func (s *Scraper) Parse(rawURL string) ([]model.MediaItem, error) {
	if ExtractPlaylistID(rawURL) != "" {
		return s.parsePlaylist(rawURL)
	}

	videoID := s.extractVideoID(rawURL)
	if videoID == "" {
		return nil, fmt.Errorf("youtube: could not extract video ID from URL")
	}

	video, err := s.video.GetVideoContext(context.Background(), videoID)
	if err != nil {
		return nil, fmt.Errorf("youtube: failed to fetch video info: %w", err)
	}

	title := video.Title
	if title == "" {
		title = videoID
	}

	return []model.MediaItem{{
		URL:   videoWatchURL(videoID),
		Title: title,
		Size:  estimatedSize(video),
		Type:  model.MediaTypeVideo,
	}}, nil
}

// parsePlaylist returns one MediaItem per video in the playlist. Playlist
// entries only carry ID/title/author metadata (per GetPlaylistContext's own
// docs), not format info, so each item's Size is left at 0 (unknown) here;
// it's known once the video is actually resolved at download time.
func (s *Scraper) parsePlaylist(rawURL string) ([]model.MediaItem, error) {
	playlist, err := s.video.GetPlaylistContext(context.Background(), rawURL)
	if err != nil {
		return nil, fmt.Errorf("youtube: failed to fetch playlist info: %w", err)
	}

	items := make([]model.MediaItem, 0, len(playlist.Videos))
	for _, entry := range playlist.Videos {
		if entry == nil || entry.ID == "" {
			continue
		}
		title := entry.Title
		if title == "" {
			title = entry.ID
		}
		items = append(items, model.MediaItem{
			URL:   videoWatchURL(entry.ID),
			Title: title,
			Type:  model.MediaTypeVideo,
		})
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("youtube: no downloadable videos found in playlist")
	}
	return items, nil
}

// estimatedSize sums the content lengths of the formats selectBestFormats
// would pick, for display before any download starts. 0 if unknown.
func estimatedSize(video *ytdl.Video) int64 {
	videoFmt, audioFmt := selectBestFormats(video)
	var size int64
	if videoFmt != nil {
		size += videoFmt.ContentLength
	}
	if audioFmt != nil {
		size += audioFmt.ContentLength
	}
	return size
}

// selectBestFormats picks the highest-resolution video format available
// (ties broken by bitrate) and, if needed, prefers the default-language
// audio-only track before comparing bitrate.
func selectBestFormats(video *ytdl.Video) (videoFmt, audioFmt *ytdl.Format) {
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.Height == 0 {
			continue
		}
		if videoFmt == nil || f.Height > videoFmt.Height ||
			(f.Height == videoFmt.Height && f.Bitrate > videoFmt.Bitrate) {
			videoFmt = f
		}
	}
	if videoFmt == nil || videoFmt.AudioChannels > 0 {
		return videoFmt, nil
	}
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.AudioChannels == 0 || f.Height != 0 {
			continue
		}
		if betterAudioFormat(f, audioFmt) {
			audioFmt = f
		}
	}
	if audioFmt == nil {
		for i := range video.Formats {
			f := &video.Formats[i]
			if f.AudioChannels > 0 && betterAudioFormat(f, audioFmt) {
				audioFmt = f
			}
		}
	}
	return videoFmt, audioFmt
}

func betterAudioFormat(candidate, current *ytdl.Format) bool {
	if current == nil {
		return true
	}
	candidateDefault := candidate.AudioTrack != nil && candidate.AudioTrack.AudioIsDefault
	currentDefault := current.AudioTrack != nil && current.AudioTrack.AudioIsDefault
	if candidateDefault != currentDefault {
		return candidateDefault
	}
	return candidate.Bitrate > current.Bitrate
}

func formatsForDownload(video *ytdl.Video) (videoFmt, audioFmt *ytdl.Format, err error) {
	videoFmt, audioFmt = selectBestFormats(video)
	if videoFmt == nil {
		return nil, nil, fmt.Errorf("youtube: no video formats found for %s", video.ID)
	}
	if videoFmt.AudioChannels == 0 && audioFmt == nil {
		return nil, nil, fmt.Errorf("youtube: no audio format found for %s", video.ID)
	}
	return videoFmt, audioFmt, nil
}

// downloadBest resolves videoID's highest-quality formats and downloads
// (muxing with ffmpeg if needed) the result to dest. onProgress, if
// non-nil, is called with cumulative bytes read and the combined total.
func (s *Scraper) downloadBest(ctx context.Context, videoID, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, onProgress func(bytes, total int64)) error {
	video, err := s.video.GetVideoContext(ctx, videoID)
	if err != nil {
		return fmt.Errorf("youtube: failed to fetch video info: %w", err)
	}

	title := video.Title
	if title == "" {
		title = videoID
	}

	videoFmt, audioFmt, err := formatsForDownload(video)
	if err != nil {
		return err
	}

	if audioFmt == nil {
		return s.downloadStreamDirect(ctx, video, videoFmt, title, dest, owner, paths, limiter, skipChecksums, onProgress)
	}
	return s.downloadAndMux(ctx, video, videoFmt, audioFmt, title, dest, owner, paths, limiter, skipChecksums, onProgress)
}

func (s *Scraper) downloadStreamDirect(ctx context.Context, video *ytdl.Video, format *ytdl.Format, title, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, onProgress func(int64, int64)) error {
	ext := "mp4"
	if strings.Contains(format.MimeType, "webm") {
		ext = "webm"
	}
	finalPath := paths.Claim(filepath.Join(dest, sanitizeFilename(title)+"."+ext), owner)
	skip, err := prepareOutput(finalPath, skipChecksums)
	if err != nil {
		return err
	}
	if skip {
		if onProgress != nil && format.ContentLength > 0 {
			onProgress(format.ContentLength, format.ContentLength)
		}
		return nil
	}
	tempPath := finalPath + ".part"
	defer os.Remove(tempPath)

	if err := s.downloadStreamToFileProgress(ctx, video, format, tempPath, limiter, onProgress); err != nil {
		return err
	}
	return commitOutput(tempPath, finalPath, skipChecksums)
}

func (s *Scraper) downloadAndMux(ctx context.Context, video *ytdl.Video, videoFmt, audioFmt *ytdl.Format, title, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, onProgress func(int64, int64)) error {
	videoExt := "mp4"
	if strings.Contains(videoFmt.MimeType, "webm") {
		videoExt = "webm"
	}
	audioExt := "m4a"
	if strings.Contains(audioFmt.MimeType, "webm") {
		audioExt = "webm"
	}

	tmpDir, err := os.MkdirTemp("", "scraper-yt-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	videoPath := filepath.Join(tmpDir, "video."+videoExt)
	audioPath := filepath.Join(tmpDir, "audio."+audioExt)
	outputPath := paths.Claim(safeOutputPath(dest, title), owner)
	skip, err := prepareOutput(outputPath, skipChecksums)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	ext := filepath.Ext(outputPath)
	tempOutputPath := strings.TrimSuffix(outputPath, ext) + ".part" + ext
	defer os.Remove(tempOutputPath)

	videoSize := videoFmt.ContentLength
	audioSize := audioFmt.ContentLength
	if videoSize <= 0 {
		videoSize = 1
	}
	if audioSize <= 0 {
		audioSize = 1
	}
	combinedTotal := videoSize + audioSize

	var videoRead, audioRead int64
	report := func() {
		if onProgress != nil {
			onProgress(videoRead+audioRead, combinedTotal)
		}
	}

	// Download video stream.
	if err := s.downloadStreamToFileProgress(ctx, video, videoFmt, videoPath, limiter, func(n, _ int64) {
		videoRead = n
		report()
	}); err != nil {
		return fmt.Errorf("youtube: failed to download video: %w", err)
	}

	// Download audio stream.
	if err := s.downloadStreamToFileProgress(ctx, video, audioFmt, audioPath, limiter, func(n, _ int64) {
		audioRead = n
		report()
	}); err != nil {
		return fmt.Errorf("youtube: failed to download audio: %w", err)
	}

	// Mux with ffmpeg — report as nearly complete while muxing.
	if onProgress != nil {
		onProgress(combinedTotal-1, combinedTotal)
	}
	// #nosec G204 -- arguments are passed directly without shell evaluation; paths are internally allocated.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-i", videoPath,
		"-i", audioPath,
		"-c:v", "copy",
		"-c:a", "aac",
		"-movflags", "+faststart",
		tempOutputPath,
	)
	cmd.Stderr = nil
	cmd.Stdout = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("youtube: ffmpeg mux failed: %w", err)
	}
	if err := commitOutput(tempOutputPath, outputPath, skipChecksums); err != nil {
		return err
	}

	if onProgress != nil {
		onProgress(combinedTotal, combinedTotal)
	}
	return nil
}

func (s *Scraper) downloadStreamToFileProgress(ctx context.Context, video *ytdl.Video, format *ytdl.Format, path string, limiter *rate.Limiter, onProgress func(int64, int64)) error {
	stream, size, err := s.video.GetStreamContext(ctx, video, format)
	if err != nil {
		return err
	}
	defer stream.Close()

	// #nosec G304 -- path is internally allocated under the configured output or private temp directory.
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()

	var read int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := stream.Read(buf)
		if n > 0 {
			if limiter != nil {
				if err := limiter.WaitN(ctx, n); err != nil {
					return err
				}
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			read += int64(n)
			if onProgress != nil {
				onProgress(read, size)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func prepareOutput(path string, skipChecksums bool) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, os.Remove(path)
	}
	if skipChecksums {
		return true, nil
	}
	present, matches, err := fileutil.VerifyChecksum(path)
	if err != nil {
		return false, err
	}
	if !present || matches {
		return true, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	if err := os.Remove(fileutil.ChecksumPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func commitOutput(tempPath, finalPath string, skipChecksums bool) error {
	if err := os.Rename(tempPath, finalPath); err != nil {
		return err
	}
	if !skipChecksums {
		if err := fileutil.WriteChecksum(finalPath); err != nil {
			return fmt.Errorf("write checksum for %s: %w", finalPath, err)
		}
	}
	return nil
}

func (s *Scraper) ExtractVideoID(rawURL string) string {
	return s.extractVideoID(rawURL)
}

func (s *Scraper) extractVideoID(rawURL string) string {
	if strings.Contains(rawURL, "v=") {
		parts := strings.Split(rawURL, "v=")
		if len(parts) < 2 {
			return ""
		}
		return strings.Split(parts[1], "&")[0]
	}
	if strings.Contains(rawURL, "youtu.be/") {
		parts := strings.Split(rawURL, "youtu.be/")
		if len(parts) < 2 {
			return ""
		}
		return strings.Split(parts[1], "?")[0]
	}
	return ""
}

// ExtractPlaylistID returns the "list" query parameter from rawURL, or ""
// if there isn't one (a plain video URL, or an unparseable string).
func ExtractPlaylistID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("list")
}

// videoWatchURL builds a canonical watch URL from a bare video ID, so
// MediaItem.URL is always something extractVideoID can round-trip.
func videoWatchURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + videoID
}

// sanitizeFilename removes characters that are invalid in filenames. It also
// rejects "." and ".." (once slashes are stripped, these are the only
// remaining path segments that could otherwise let a crafted video title
// make the joined download path escape the destination directory).
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

// safeOutputPath joins dest and the sanitized title into an absolute .mp4
// path. Making it absolute ensures that even if dest is relative (e.g. ".")
// and the sanitized title happens to start with "-", ffmpeg's argument
// parser can never misread the muxed output path as a flag.
func safeOutputPath(dest, title string) string {
	p := filepath.Join(dest, sanitizeFilename(title)+".mp4")
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// --- Downloader ---

const (
	progressBufSize     = 4096
	minRateLimitBurst   = 32 * 1024
	defaultReadTimeout  = 30 * time.Second
	defaultYouTubeAgent = "Scraper/1.0"
)

type DownloaderConfig struct {
	Concurrency  int
	Timeout      time.Duration
	UserAgent    string
	RateLimit    int64
	SkipChecksum bool
}

// Downloader adapts Scraper to the dashboard's Downloader interface (see
// internal/ui/dashboard), so YouTube videos — whether from a single URL or
// enumerated from a playlist — flow through the same batch download UI as
// every other site. Each Download call resolves and fetches the
// highest-quality format available for that item, muxing video+audio with
// ffmpeg when they aren't already combined in one stream.
type Downloader struct {
	scraper   *Scraper
	progress  chan model.ProgressEvent
	mu        sync.Mutex
	cancelMap map[string]context.CancelFunc
	queued    map[string]struct{}
	cancelled map[string]struct{}
	sem       chan struct{}
	paths     *fileutil.PathAllocator
	limiter   *rate.Limiter
	skipSum   bool
}

// NewDownloader creates a Downloader that resolves and downloads videos via
// scraper, running at most concurrency downloads (and ffmpeg mux processes)
// at once.
func NewDownloader(scraper *Scraper, concurrency int) *Downloader {
	return NewDownloaderWithConfig(scraper, DownloaderConfig{Concurrency: concurrency})
}

func NewDownloaderWithConfig(scraper *Scraper, config DownloaderConfig) *Downloader {
	if config.Concurrency < 1 {
		config.Concurrency = 1
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultReadTimeout
	}
	if config.UserAgent == "" {
		config.UserAgent = defaultYouTubeAgent
	}
	scraper.video.HTTPClient = netutil.WithUserAgent(netutil.NewPublicStreamingClient(config.Timeout), config.UserAgent)

	var limiter *rate.Limiter
	if config.RateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(config.RateLimit), minRateLimitBurst)
	}
	return &Downloader{
		scraper:   scraper,
		progress:  make(chan model.ProgressEvent, progressBufSize),
		cancelMap: make(map[string]context.CancelFunc),
		queued:    make(map[string]struct{}),
		cancelled: make(map[string]struct{}),
		sem:       make(chan struct{}, config.Concurrency),
		paths:     fileutil.NewPathAllocator(),
		limiter:   limiter,
		skipSum:   config.SkipChecksum,
	}
}

// Compile-time check that Downloader satisfies the dashboard's Downloader
// interface shape (declared inline rather than importing
// internal/ui/dashboard, which would invert the dependency direction).
var _ interface {
	Prepare(item model.MediaItem)
	Download(item model.MediaItem, dest string) error
	Cancel(item model.MediaItem)
	Progress() <-chan model.ProgressEvent
} = (*Downloader)(nil)

func (d *Downloader) Prepare(item model.MediaItem) {
	d.mu.Lock()
	d.queued[item.Key()] = struct{}{}
	d.mu.Unlock()
}

func (d *Downloader) Download(item model.MediaItem, dest string) error {
	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	delete(d.queued, item.Key())
	if _, ok := d.cancelled[item.Key()]; ok {
		delete(d.cancelled, item.Key())
		d.mu.Unlock()
		cancel()
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		return fmt.Errorf("download cancelled: %s", item.URL)
	}
	d.cancelMap[item.Key()] = cancel
	d.mu.Unlock()
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

	d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusDownloading, Start: time.Now()})

	videoID := d.scraper.ExtractVideoID(item.URL)
	if videoID == "" {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
		return fmt.Errorf("youtube: could not extract video ID from %s", item.URL)
	}

	err := d.scraper.downloadBest(ctx, videoID, dest, item.Key(), d.paths, d.limiter, d.skipSum, func(bytes, total int64) {
		d.sendProgress(model.ProgressEvent{Item: item, Bytes: bytes, Total: total, Status: model.StatusDownloading})
	})

	switch {
	case err == nil:
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCompleted})
		return nil
	case ctx.Err() != nil:
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled})
		return fmt.Errorf("download cancelled: %s", item.URL)
	default:
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed})
		return err
	}
}

// Cancel cancels an active download for the given item, aborting an
// in-flight stream fetch or ffmpeg mux right away rather than waiting for
// it to finish.
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

// Progress returns a read-only channel of download progress events.
func (d *Downloader) Progress() <-chan model.ProgressEvent {
	return d.progress
}

func (d *Downloader) sendProgress(evt model.ProgressEvent) {
	select {
	case d.progress <- evt:
	default:
	}
}

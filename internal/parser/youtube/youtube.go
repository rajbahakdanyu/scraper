// internal/parser/youtube/youtube.go
package youtube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	"golang.org/x/sys/unix"
	"golang.org/x/time/rate"
)

type Scraper struct {
	video *ytdl.Client

	// parsed holds video info already fetched by Parse, keyed by video ID,
	// so the download path reuses it instead of fetching the same metadata
	// a second time. Guarded by mu because downloads run concurrently.
	mu     sync.Mutex
	parsed map[string]*ytdl.Video
}

// NewYouTubeScraper creates a YouTube scraper using the kkdai/youtube library
// for real stream URL extraction and downloads.
func NewYouTubeScraper() *Scraper {
	client, _ := netutil.NewPublicClient(defaultReadTimeout)
	return &Scraper{
		video:  &ytdl.Client{HTTPClient: client},
		parsed: make(map[string]*ytdl.Video),
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

	// Hand the info to the download path (see takeParsedVideo) so the same
	// video's metadata isn't fetched again when it is downloaded.
	s.mu.Lock()
	s.parsed[videoID] = video
	s.mu.Unlock()

	title := video.Title
	if title == "" {
		title = videoID
	}

	return []model.MediaItem{{
		URL:   videoWatchURL(videoID),
		Title: title,
		Size:  estimatedSize(video, 0),
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

// estimatedSize sums the content lengths of the formats selectFormats
// would pick, for display before any download starts. 0 if unknown.
func estimatedSize(video *ytdl.Video, maxHeight int) int64 {
	videoFmt, audioFmt := selectFormats(video, maxHeight)
	var size int64
	if videoFmt != nil {
		size += videoFmt.ContentLength
	}
	if audioFmt != nil {
		size += audioFmt.ContentLength
	}
	return size
}

// selectFormats picks the best video format (capped at maxHeight when set,
// 0 = no cap) and the best audio-only format. When maxHeight is set and
// no format fits, the displayable best is returned with a stderr warning.
func selectFormats(video *ytdl.Video, maxHeight int) (videoFmt, audioFmt *ytdl.Format) {
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.Height == 0 {
			continue
		}
		if maxHeight > 0 && f.Height > maxHeight {
			continue
		}
		if videoFmt == nil || f.Height > videoFmt.Height ||
			(f.Height == videoFmt.Height && f.Bitrate > videoFmt.Bitrate) {
			videoFmt = f
		}
	}
	if videoFmt == nil && maxHeight > 0 {
		videoFmt, _ = selectFormats(video, 0)
		if videoFmt != nil {
			fmt.Fprintf(os.Stderr, "youtube: quality %dp not available, falling back to %dp\n", maxHeight, videoFmt.Height)
		}
	}
	if videoFmt == nil || videoFmt.AudioChannels > 0 {
		return videoFmt, nil
	}
	audioFmt = selectBestAudio(video)
	return videoFmt, audioFmt
}

// selectBestAudio picks the best audio-only format (Height==0,
// AudioChannels>0): prefers the default audio track, then highest bitrate.
func selectBestAudio(video *ytdl.Video) *ytdl.Format {
	var best *ytdl.Format
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.AudioChannels == 0 || f.Height != 0 {
			continue
		}
		if betterAudioFormat(f, best) {
			best = f
		}
	}
	if best == nil {
		for i := range video.Formats {
			f := &video.Formats[i]
			if f.AudioChannels > 0 && betterAudioFormat(f, best) {
				best = f
			}
		}
	}
	return best
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

// formatsForDownload validates the selected format pair. When audioOnly is
// true, videoFmt is always nil — only audio matters.
func formatsForDownload(video *ytdl.Video, maxHeight int, audioOnly bool) (vidFmt, audFmt *ytdl.Format, err error) {
	if audioOnly {
		audFmt = selectBestAudio(video)
		if audFmt == nil {
			vidFmt, _ = selectFormats(video, 0)
			if vidFmt == nil {
				return nil, nil, fmt.Errorf("youtube: no audio format found for %s", video.ID)
			}
			return vidFmt, nil, nil
		}
		return nil, audFmt, nil
	}
	vidFmt, audFmt = selectFormats(video, maxHeight)
	if vidFmt == nil {
		return nil, nil, fmt.Errorf("youtube: no video formats found for %s", video.ID)
	}
	if vidFmt.AudioChannels == 0 && audFmt == nil {
		return nil, nil, fmt.Errorf("youtube: no audio format found for %s", video.ID)
	}
	return vidFmt, audFmt, nil
}

// takeParsedVideo returns the video info Parse already fetched for videoID,
// or nil when there is none cached (playlist items only get resolved here at
// download time). The entry is removed as it is claimed: that bounds the
// map to info nobody has downloaded yet, and a retry attempt after a
// transient failure re-fetches fresh metadata (and stream URLs) just as
// every attempt did before the info was reused.
func (s *Scraper) takeParsedVideo(videoID string) *ytdl.Video {
	s.mu.Lock()
	defer s.mu.Unlock()
	video := s.parsed[videoID]
	delete(s.parsed, videoID)
	return video
}

// downloadBest resolves videoID's formats and downloads (muxing or
// extracting audio with ffmpeg if needed) the result to dest. maxHeight caps
// video resolution (0 = no cap). audioOnly, if non-empty ("m4a" or "mp3"),
// bypasses video and downloads/extracts only audio.
func (s *Scraper) downloadBest(ctx context.Context, videoID, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, maxHeight int, audioOnly string, onProgress func(bytes, total int64)) error {
	video := s.takeParsedVideo(videoID)
	if video == nil {
		var err error
		video, err = s.video.GetVideoContext(ctx, videoID)
		if err != nil {
			return fmt.Errorf("youtube: failed to fetch video info: %w", err)
		}
	}

	title := video.Title
	if title == "" {
		title = videoID
	}

	videoFmt, audioFmt, err := formatsForDownload(video, maxHeight, audioOnly != "")
	if err != nil {
		// The video has no downloadable formats (region-locked,
		// members-only, audio-less, ...) — refetching the same metadata
		// on a retry won't change that.
		return model.NewNonRetryable(err)
	}

	if audioOnly != "" {
		return s.downloadAudioOnly(ctx, video, videoFmt, audioFmt, title, dest, owner, paths, limiter, skipChecksums, audioOnly, onProgress)
	}
	if audioFmt == nil {
		return s.downloadStreamDirect(ctx, video, videoFmt, title, dest, owner, paths, limiter, skipChecksums, onProgress)
	}
	return s.downloadAndMux(ctx, video, videoFmt, audioFmt, title, dest, owner, paths, limiter, skipChecksums, onProgress)
}

func (s *Scraper) downloadAudioOnly(ctx context.Context, video *ytdl.Video, videoFmt, audioFmt *ytdl.Format, title, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, audioFormat string, onProgress func(int64, int64)) error {
	var srcFormat *ytdl.Format
	if audioFmt != nil {
		srcFormat = audioFmt
	} else {
		srcFormat = videoFmt
	}

	ext := "m4a"
	if audioFormat == "mp3" {
		ext = "mp3"
	}
	finalPath := safeOutputPath(dest, title)
	if ext != "mp4" {
		finalPath = strings.TrimSuffix(finalPath, filepath.Ext(finalPath)) + "." + ext
	}
	finalPath = paths.Claim(finalPath, owner)
	skip, err := prepareOutput(finalPath, skipChecksums)
	if err != nil {
		return err
	}
	if skip {
		if onProgress != nil && srcFormat.ContentLength > 0 {
			onProgress(srcFormat.ContentLength, srcFormat.ContentLength)
		}
		return nil
	}
	tempPath := finalPath + ".part"
	defer os.Remove(tempPath)

	streamTemp := tempPath + ".stream"
	defer os.Remove(streamTemp)

	if err := s.downloadStreamToFileProgress(ctx, video, srcFormat, streamTemp, limiter, func(n, total int64) {
		if onProgress != nil {
			onProgress(n, total)
		}
	}); err != nil {
		return err
	}

	if err := extractAudio(ctx, streamTemp, tempPath, audioFormat); err != nil {
		return err
	}
	return commitOutput(tempPath, finalPath, skipChecksums)
}

func (s *Scraper) downloadStreamDirect(ctx context.Context, video *ytdl.Video, format *ytdl.Format, title, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, onProgress func(int64, int64)) error {
	ext := "mp4"
	if strings.Contains(format.MimeType, "webm") {
		ext = "webm"
	}
	finalPath := safeOutputPath(dest, title)
	if ext != "mp4" {
		finalPath = strings.TrimSuffix(finalPath, filepath.Ext(finalPath)) + "." + ext
	}
	finalPath = paths.Claim(finalPath, owner)
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
		return model.NewNonRetryable(err)
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
	if err := muxStreams(ctx, videoPath, audioPath, tempOutputPath, audioCopyable(audioFmt)); err != nil {
		return err
	}
	if err := commitOutput(tempOutputPath, outputPath, skipChecksums); err != nil {
		return err
	}

	if onProgress != nil {
		onProgress(combinedTotal, combinedTotal)
	}
	return nil
}

// audioCopyable reports whether format's audio is AAC-in-mp4 (e.g.
// codecs="mp4a.40.2") and can therefore be stream-copied into the mp4
// output container. Anything else (opus in webm, ...) must be transcoded
// to AAC at mux time.
func audioCopyable(format *ytdl.Format) bool {
	if format == nil {
		return false
	}
	mime := strings.ToLower(format.MimeType)
	return strings.Contains(mime, "audio/mp4") && strings.Contains(mime, "mp4a")
}

// muxStreams combines a separate video and audio stream into outputPath
// with ffmpeg. Video is always stream-copied; audio is stream-copied when
// audioCopy is true (already AAC) and transcoded to AAC otherwise.
// ffmpeg's stderr is captured and its last line included in the returned
// error — a bare "exit status 1" tells the user nothing about why the mux
// failed.
func muxStreams(ctx context.Context, videoPath, audioPath, outputPath string, audioCopy bool) error {
	var stderr bytes.Buffer
	audioCodec := "aac"
	if audioCopy {
		audioCodec = "copy"
	}
	// #nosec G204 -- arguments are passed directly without shell evaluation; paths are internally allocated.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-i", videoPath,
		"-i", audioPath,
		"-c:v", "copy",
		"-c:a", audioCodec,
		"-movflags", "+faststart",
		outputPath,
	)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// A failure to even start ffmpeg (binary missing, not executable)
		// is permanent — retrying the download can't install ffmpeg. An
		// ExitError means ffmpeg ran and rejected the inputs (e.g. a
		// truncated stream); a retry downloads fresh bytes and can
		// succeed, so that stays retryable.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return model.NewNonRetryable(fmt.Errorf("youtube: failed to start ffmpeg: %w", err))
		}
		detail := strings.TrimSpace(stderr.String())
		if idx := strings.LastIndex(detail, "\n"); idx >= 0 {
			detail = detail[idx+1:]
		}
		if detail == "" {
			return fmt.Errorf("youtube: ffmpeg mux failed: %w", err)
		}
		return fmt.Errorf("youtube: ffmpeg mux failed: %w: %s", err, detail)
	}
	return nil
}

// extractAudio extracts or transcodes audio from srcPath to outputPath.
// For m4a: stream-copies audio (-vn -acodec copy). For mp3: transcodes to
// mp3 via libmp3lame (-vn -acodec libmp3lame -q:a 2).
func extractAudio(ctx context.Context, srcPath, outputPath, format string) error {
	var stderr bytes.Buffer
	args := []string{"-y", "-i", srcPath, "-vn"}
	if format == "mp3" {
		args = append(args, "-codec:a", "libmp3lame", "-q:a", "2")
	} else {
		args = append(args, "-acodec", "copy")
	}
	args = append(args, outputPath)
	// #nosec G204 -- arguments are passed without shell evaluation; paths are internally allocated.
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return model.NewNonRetryable(fmt.Errorf("youtube: failed to start ffmpeg: %w", err))
		}
		stderrLines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		lastLine := ""
		if len(stderrLines) > 0 {
			lastLine = stderrLines[len(stderrLines)-1]
		}
		return fmt.Errorf("youtube: ffmpeg exited with code %d: %s", exitErr.ExitCode(), lastLine)
	}
	return nil
}

func (s *Scraper) downloadStreamToFileProgress(ctx context.Context, video *ytdl.Video, format *ytdl.Format, path string, limiter *rate.Limiter, onProgress func(int64, int64)) error {
	stream, size, err := s.video.GetStreamContext(ctx, video, format)
	if err != nil {
		return err
	}
	defer stream.Close()
	return copyStreamToFile(ctx, stream, size, path, limiter, onProgress)
}

// copyStreamToFile writes stream to path, reporting progress along the way.
// When size > 0 the stream must deliver exactly that many bytes: a stream
// that ends early (io.EOF before size bytes) is rejected as truncated so a
// partial file can never be committed and blessed with a checksum sidecar.
func copyStreamToFile(ctx context.Context, stream io.Reader, size int64, path string, limiter *rate.Limiter, onProgress func(int64, int64)) error {
	// #nosec G304 -- path is internally allocated under the configured output or private temp directory.
	fd, err := unix.Open(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|unix.O_NOFOLLOW, 0644)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return model.NewNonRetryable(fmt.Errorf("refusing to follow symlink for .part file %s: %w", path, err))
		}
		// Local filesystem failures are permanent — a retry can't make
		// the destination writable.
		return model.NewNonRetryable(err)
	}
	out := os.NewFile(uintptr(fd), path)
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
				return model.NewNonRetryable(werr)
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
		return model.NewNonRetryable(err)
	}
	closed = true
	if size > 0 && read != size {
		return fmt.Errorf("download size mismatch: got %d bytes, want %d (stream truncated)", read, size)
	}
	return nil
}

// prepareOutput's failures are all local filesystem operations, so every
// error it returns is wrapped as nonRetryable: retrying the download can't
// fix a filesystem problem.
func prepareOutput(path string, skipChecksums bool) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, model.NewNonRetryable(err)
	}
	if info.Size() == 0 {
		if err := os.Remove(path); err != nil {
			return false, model.NewNonRetryable(err)
		}
		return false, nil
	}
	if skipChecksums {
		return true, nil
	}
	present, matches, err := fileutil.VerifyChecksum(path)
	if err != nil {
		return false, model.NewNonRetryable(err)
	}
	if !present || matches {
		return true, nil
	}
	if err := os.Remove(path); err != nil {
		return false, model.NewNonRetryable(err)
	}
	if err := os.Remove(fileutil.ChecksumPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, model.NewNonRetryable(err)
	}
	return false, nil
}

// commitOutput's failures are all local filesystem operations, so every
// error it returns is wrapped as nonRetryable (see prepareOutput).
func commitOutput(tempPath, finalPath string, skipChecksums bool) error {
	if err := os.Rename(tempPath, finalPath); err != nil {
		return model.NewNonRetryable(err)
	}
	if !skipChecksums {
		if err := fileutil.WriteChecksum(finalPath); err != nil {
			return model.NewNonRetryable(fmt.Errorf("write checksum for %s: %w", finalPath, err))
		}
	}
	return nil
}

func (s *Scraper) ExtractVideoID(rawURL string) string {
	return s.extractVideoID(rawURL)
}

// extractVideoID pulls the video ID out of a YouTube URL. It handles the
// modern URL shapes (/watch?v=, youtu.be, /shorts/, /embed/, /live/) by
// parsing the URL properly rather than substring-matching "v=" — the old
// approach both missed the path-based shapes and false-positived on
// unrelated query parameters that merely contain "v=" (e.g. ?iv=...).
func (s *Scraper) extractVideoID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	// Query-parameter form — checked for any host (including schemeless
	// input like "watch?v=ID") since "v" is a precise parameter name.
	if v := u.Query().Get("v"); v != "" {
		return v
	}

	host := strings.ToLower(u.Hostname())
	isYouTube := host == "youtu.be" ||
		host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") ||
		host == "youtube-nocookie.com" || strings.HasSuffix(host, ".youtube-nocookie.com")
	if !isYouTube {
		return ""
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if host == "youtu.be" {
		return segments[0]
	}
	// /shorts/ID, /embed/ID, /live/ID, /v/ID
	if len(segments) == 2 {
		switch segments[0] {
		case "shorts", "embed", "live", "v":
			return segments[1]
		}
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

// safeOutputPath joins dest and the sanitized title into an absolute .mp4
// path. Making it absolute ensures that even if dest is relative (e.g. ".")
// and the sanitized title happens to start with "-", ffmpeg's argument
// parser can never misread the muxed output path as a flag.
func safeOutputPath(dest, title string) string {
	filename := fileutil.SanitizeFilename(title)
	if !strings.HasSuffix(strings.ToLower(title), ".mp4") {
		filename += ".mp4"
	}
	p := filepath.Join(dest, filename)
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
	maxRetries          = 3
	retryBaseDelay      = 500 * time.Millisecond
)

// withRetry runs fn up to maxRetries times with exponential backoff +
// jitter between attempts, mirroring the generic downloader's policy: a
// single transient network blip shouldn't fail a whole video. A cancelled
// context stops the loop immediately instead of sleeping through backoff,
// and a model.NonRetryableError (a permanent failure retrying can't fix) stops
// it after the first attempt.
func withRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(netutil.BackoffWithJitter(retryBaseDelay, attempt)):
			}
		}
		if lastErr = fn(); lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			break
		}
		var nre *model.NonRetryableError
		if errors.As(lastErr, &nre) {
			// Permanent failure (no formats, missing ffmpeg, local
			// filesystem): another attempt would just re-download the
			// same bytes to fail the same way.
			break
		}
	}
	return lastErr
}

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
	quality   int    // maxHeight; 0 = no cap
	audioOnly string // "" or "m4a"/"mp3"
	// wg tracks in-flight Download calls. Shutdown closes the progress
	// channel only after wg drains, so a sendProgress can never hit a
	// closed channel. shuttingDown (guarded by mu) makes post-Shutdown
	// Download calls fail fast instead of joining wg.
	wg           sync.WaitGroup
	shuttingDown bool
}

func NewDownloaderWithConfig(scraper *Scraper, config DownloaderConfig) (*Downloader, error) {
	if config.Concurrency < 1 {
		config.Concurrency = 1
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultReadTimeout
	}
	if config.UserAgent == "" {
		config.UserAgent = defaultYouTubeAgent
	}
	if config.ProxyURL != "" {
		httpClient, err := netutil.NewPublicStreamingClientWithProxy(config.Timeout, config.ProxyURL)
		if err != nil {
			return nil, err
		}
		scraper.video.HTTPClient = netutil.WithUserAgent(httpClient, config.UserAgent)
		scraper.video.HTTPClient = netutil.WithCookies(scraper.video.HTTPClient, config.Cookies)
	} else {
		httpClient, _ := netutil.NewPublicStreamingClient(config.Timeout)
		scraper.video.HTTPClient = netutil.WithUserAgent(httpClient, config.UserAgent)
		scraper.video.HTTPClient = netutil.WithCookies(scraper.video.HTTPClient, config.Cookies)
	}

	var limiter *rate.Limiter
	if config.RateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(config.RateLimit), minRateLimitBurst)
	}
	var maxHeight int
	if config.Quality != "" {
		maxHeight = parseQualityHeight(config.Quality)
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
		quality:   maxHeight,
		audioOnly: config.AudioOnly,
	}, nil
}

var qualityHeights = map[string]int{
	"2160p": 2160,
	"1440p": 1440,
	"1080p": 1080,
	"720p":  720,
	"480p":  480,
	"360p":  360,
	"240p":  240,
}

var validQualities = map[string]bool{
	"": true, "2160p": true, "1440p": true, "1080p": true,
	"720p": true, "480p": true, "360p": true, "240p": true,
}

func ValidQuality(q string) (int, bool) {
	if !validQualities[q] {
		return 0, false
	}
	if q == "" {
		return 0, true
	}
	return qualityHeights[q], true
}

var qualityHeightsReverse = map[int]string{
	2160: "2160p",
	1440: "1440p",
	1080: "1080p",
	720:  "720p",
	480:  "480p",
	360:  "360p",
	240:  "240p",
}

func parseQualityHeight(q string) int {
	return qualityHeights[q]
}

// Compile-time check that Downloader satisfies the dashboard's Downloader
// interface shape (declared inline rather than importing
// internal/ui/dashboard, which would invert the dependency direction).
var _ interface {
	Prepare(item model.MediaItem)
	Download(item model.MediaItem, dest string) error
	Cancel(item model.MediaItem)
	Shutdown()
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
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled, Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})
		d.wg.Done()
		return &model.CancelledError{URL: item.URL}
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
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled, Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})
		return &model.CancelledError{URL: item.URL}
	}

	d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusDownloading, Start: time.Now(), Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})

	videoID := d.scraper.ExtractVideoID(item.URL)
	if videoID == "" {
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed, Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})
		return fmt.Errorf("youtube: could not extract video ID from %s", item.URL)
	}

	err := withRetry(ctx, func() error {
		return d.scraper.downloadBest(ctx, videoID, dest, item.Key(), d.paths, d.limiter, d.skipSum, d.quality, d.audioOnly, func(bytes, total int64) {
			d.sendProgress(model.ProgressEvent{Item: item, Bytes: bytes, Total: total, Status: model.StatusDownloading, Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})
		})
	})

	switch {
	case err == nil:
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCompleted, Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})
		return nil
	case ctx.Err() != nil:
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusCancelled, Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})
		return &model.CancelledError{URL: item.URL}
	default:
		d.sendProgress(model.ProgressEvent{Item: item, Status: model.StatusFailed, Quality: qualityHeightsReverse[d.quality], AudioOnly: d.audioOnly})
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

// Shutdown cancels every active download — aborting in-flight stream
// fetches, ffmpeg muxes, and downloads still waiting on the concurrency
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

func (d *Downloader) sendProgress(evt model.ProgressEvent) {
	select {
	case d.progress <- evt:
	default:
	}
}

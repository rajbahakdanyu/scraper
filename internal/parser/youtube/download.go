package youtube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/downloadercore"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"golang.org/x/time/rate"
)

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
func (s *Scraper) downloadBest(ctx context.Context, videoID, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, maxHeight int, audioOnly string, onProgress func(bytes, total int64)) (string, error) {
	video := s.takeParsedVideo(videoID)
	if video == nil {
		var err error
		video, err = s.getVideo(ctx, videoID)
		if err != nil {
			return "", fmt.Errorf("youtube: failed to fetch video info: %w", err)
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
		return "", model.NewNonRetryable(err)
	}
	if s.warn != nil && audioOnly == "" && maxHeight > 0 && videoFmt.Height > maxHeight {
		s.warn(fmt.Sprintf("youtube: %s: %dp not available, using %dp", title, maxHeight, videoFmt.Height))
	}
	identity := youtubeOutputIdentity(videoID, maxHeight, audioOnly)

	if audioOnly != "" {
		err := s.downloadAudioOnly(ctx, video, videoFmt, audioFmt, title, dest, owner, paths, limiter, skipChecksums, audioOnly, identity, onProgress)
		return paths.Load(owner), err
	}
	if audioFmt == nil {
		err := s.downloadStreamDirect(ctx, video, videoFmt, title, dest, owner, paths, limiter, skipChecksums, identity, onProgress)
		return paths.Load(owner), err
	}
	err = s.downloadAndMux(ctx, video, videoFmt, audioFmt, title, dest, owner, paths, limiter, skipChecksums, identity, onProgress)
	return paths.Load(owner), err
}

func (s *Scraper) downloadAudioOnly(ctx context.Context, video *ytdl.Video, videoFmt, audioFmt *ytdl.Format, title, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, audioFormat, identity string, onProgress func(int64, int64)) (err error) {
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
	outputPath := safeOutputPath(dest, title)
	outputPath = strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "." + ext
	finalPath, err := claimYouTubeOutput(paths, outputPath, owner, identity)
	if err != nil {
		return model.NewNonRetryable(err)
	}
	skip, err := prepareOutput(finalPath, identity)
	if err != nil {
		return err
	}
	if skip {
		if onProgress != nil && srcFormat.ContentLength > 0 {
			onProgress(srcFormat.ContentLength, srcFormat.ContentLength)
		}
		return nil
	}
	workDir, err := streamWorkDir(dest, finalPath, identity)
	if err != nil {
		return model.NewNonRetryable(err)
	}
	defer func() { releaseWorkDir(workDir, err) }()

	if _, err = s.downloadStreamToFileProgress(ctx, video, srcFormat, filepath.Join(workDir, workSource), limiter, onProgress); err != nil {
		return err
	}

	extracted, err := extractAudio(ctx, workDir, audioFormat, audioCopyable(srcFormat))
	if err != nil {
		discardStreams(ctx, workDir)
		return err
	}
	err = commitOutputForSource(filepath.Join(workDir, extracted), finalPath, skipChecksums, "", identity)
	return err
}

func (s *Scraper) downloadStreamDirect(ctx context.Context, video *ytdl.Video, format *ytdl.Format, title, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, identity string, onProgress func(int64, int64)) (err error) {
	ext := "mp4"
	if strings.Contains(format.MimeType, "webm") {
		ext = "webm"
	}
	outputPath := safeOutputPath(dest, title)
	if ext != "mp4" {
		outputPath = strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "." + ext
	}
	finalPath, err := claimYouTubeOutput(paths, outputPath, owner, identity)
	if err != nil {
		return model.NewNonRetryable(err)
	}
	skip, err := prepareOutput(finalPath, identity)
	if err != nil {
		return err
	}
	if skip {
		if onProgress != nil && format.ContentLength > 0 {
			onProgress(format.ContentLength, format.ContentLength)
		}
		return nil
	}
	workDir, err := streamWorkDir(dest, finalPath, identity)
	if err != nil {
		return model.NewNonRetryable(err)
	}
	defer func() { releaseWorkDir(workDir, err) }()

	tempPath := filepath.Join(workDir, workSource)
	checksum, err := s.downloadStreamToFileProgress(ctx, video, format, tempPath, limiter, onProgress)
	if err != nil {
		return err
	}
	err = commitOutputForSource(tempPath, finalPath, skipChecksums, checksum, identity)
	return err
}

func (s *Scraper) downloadAndMux(ctx context.Context, video *ytdl.Video, videoFmt, audioFmt *ytdl.Format, title, dest, owner string, paths *fileutil.PathAllocator, limiter *rate.Limiter, skipChecksums bool, identity string, onProgress func(int64, int64)) (err error) {
	outputPath, err := claimYouTubeOutput(paths, safeOutputPath(dest, title), owner, identity)
	if err != nil {
		return model.NewNonRetryable(err)
	}
	skip, err := prepareOutput(outputPath, identity)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	tmpDir, err := streamWorkDir(dest, outputPath, identity)
	if err != nil {
		return model.NewNonRetryable(err)
	}
	defer func() { releaseWorkDir(tmpDir, err) }()

	videoPath := filepath.Join(tmpDir, workVideo)
	audioPath := filepath.Join(tmpDir, workAudio)

	videoSize := videoFmt.ContentLength
	audioSize := audioFmt.ContentLength
	if videoSize <= 0 {
		videoSize = 1
	}
	if audioSize <= 0 {
		audioSize = 1
	}
	combinedTotal := videoSize + audioSize

	var videoRead, audioRead atomic.Int64
	report := func() {
		if onProgress != nil {
			onProgress(videoRead.Load()+audioRead.Load(), combinedTotal)
		}
	}

	// Fetch both streams at once; the first failure cancels the other.
	streamCtx, cancelStreams := context.WithCancel(ctx)
	defer cancelStreams()
	var wg sync.WaitGroup
	var videoErr, audioErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, videoErr = s.downloadStreamToFileProgress(streamCtx, video, videoFmt, videoPath, limiter, func(n, _ int64) {
			videoRead.Store(n)
			report()
		}); videoErr != nil {
			cancelStreams()
		}
	}()
	go func() {
		defer wg.Done()
		if _, audioErr = s.downloadStreamToFileProgress(streamCtx, video, audioFmt, audioPath, limiter, func(n, _ int64) {
			audioRead.Store(n)
			report()
		}); audioErr != nil {
			cancelStreams()
		}
	}()
	wg.Wait()
	if err = streamPairErr(videoErr, audioErr); err != nil {
		return err
	}

	// Mux with ffmpeg — report as nearly complete while muxing.
	if onProgress != nil {
		onProgress(combinedTotal-1, combinedTotal)
	}
	if err = muxStreams(ctx, tmpDir, audioCopyable(audioFmt)); err != nil {
		discardStreams(ctx, tmpDir)
		return err
	}
	if err = commitOutputForSource(filepath.Join(tmpDir, workMuxed), outputPath, skipChecksums, "", identity); err != nil {
		return err
	}

	if onProgress != nil {
		onProgress(combinedTotal, combinedTotal)
	}
	return nil
}

// streamPairErr reports the root cause of a failed parallel stream fetch,
// not the sibling stream its failure cancelled.
func streamPairErr(videoErr, audioErr error) error {
	switch {
	case videoErr != nil && (audioErr == nil || !errors.Is(videoErr, context.Canceled)):
		return fmt.Errorf("youtube: failed to download video: %w", videoErr)
	case audioErr != nil:
		return fmt.Errorf("youtube: failed to download audio: %w", audioErr)
	}
	return nil
}

// copyStreamToFile writes stream to path, reporting progress along the way.
// When size > 0 the stream must deliver exactly that many bytes: a stream
// that ends early (io.EOF before size bytes) is rejected as truncated so a
// partial file can never be committed and blessed with a checksum sidecar.
func copyStreamToFile(ctx context.Context, stream io.Reader, size int64, path string, limiter *rate.Limiter, onProgress func(int64, int64)) (string, error) {
	return copyStreamToFileFrom(ctx, stream, 0, size, path, limiter, onProgress)
}

// copyStreamToFileFrom appends stream to the first offset bytes already in
// path (offset 0 truncates). The returned checksum covers the whole file.
func copyStreamToFileFrom(ctx context.Context, stream io.Reader, offset, size int64, path string, limiter *rate.Limiter, onProgress func(int64, int64)) (string, error) {
	hasher := sha256.New()
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if offset > 0 {
		if err := hashFilePrefix(path, offset, hasher); err != nil {
			return "", model.NewNonRetryable(err)
		}
		flag = os.O_WRONLY | os.O_APPEND
		if onProgress != nil {
			onProgress(offset, size)
		}
	}
	f, err := fileutil.OpenFile(path, flag, 0644)
	if err != nil {
		// Local filesystem failures are permanent — a retry can't make
		// the destination writable.
		return "", model.NewNonRetryable(err)
	}
	out := f
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()

	read := offset
	reader := io.TeeReader(stream, hasher)
	buf := make([]byte, 512*1024)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if err := downloadercore.WaitN(ctx, limiter, n); err != nil {
				return "", err
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return "", model.NewNonRetryable(werr)
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
			return "", readErr
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if err := out.Close(); err != nil {
		return "", model.NewNonRetryable(err)
	}
	closed = true
	if size > 0 && read != size {
		return "", fmt.Errorf("download size mismatch: got %d bytes, want %d (stream truncated)", read, size)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// prepareOutput skips only a complete output carrying matching identity and
// integrity metadata. Files from an older version are preserved separately.
func prepareOutput(path, identity string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, model.NewNonRetryable(err)
	}
	matches, err := fileutil.VerifySource(path, identity, 0)
	if err != nil {
		return false, model.NewNonRetryable(err)
	}
	return matches, nil
}

func claimYouTubeOutput(paths *fileutil.PathAllocator, path, owner, identity string) (string, error) {
	return paths.ClaimAvailable(path, owner, func(candidate string) (bool, error) {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, nil
		}
		return fileutil.VerifySource(candidate, identity, 0)
	})
}

func youtubeOutputIdentity(videoID string, maxHeight int, audioOnly string) string {
	return fileutil.SourceIdentity(fmt.Sprintf("youtube\n%s\n%d\n%s", videoID, maxHeight, audioOnly))
}

// commitOutput's failures are all local filesystem operations, so every
// error it returns is wrapped as nonRetryable (see prepareOutput).
func commitOutput(tempPath, finalPath string, skipChecksums bool) error {
	return commitOutputForSource(tempPath, finalPath, skipChecksums, "", "")
}

func commitOutputForSource(tempPath, finalPath string, skipChecksums bool, checksum, identity string) error {
	if err := os.Rename(tempPath, finalPath); err != nil {
		return model.NewNonRetryable(err)
	}
	if !skipChecksums {
		var err error
		if checksum != "" {
			err = fileutil.WriteChecksumDigest(finalPath, checksum)
		} else {
			err = fileutil.WriteChecksum(finalPath)
		}
		if err != nil {
			return model.NewNonRetryable(fmt.Errorf("write checksum for %s: %w", finalPath, err))
		}
	}
	if identity != "" {
		if checksum == "" {
			var err error
			checksum, err = fileutil.DigestFile(finalPath)
			if err != nil {
				return model.NewNonRetryable(fmt.Errorf("hash %s: %w", finalPath, err))
			}
		}
		if err := fileutil.WriteSource(finalPath, identity, checksum); err != nil {
			return model.NewNonRetryable(fmt.Errorf("write source metadata for %s: %w", finalPath, err))
		}
	}
	return nil
}

// safeOutputPath joins dest and the sanitized title into an absolute .mp4
// path; the path is reported to frontends, and the GUI only opens absolute
// paths.
func safeOutputPath(dest, title string) string {
	filename := fileutil.SanitizeFilename(title)
	if !strings.HasSuffix(strings.ToLower(filename), ".mp4") {
		filename = fileutil.SanitizeFilename(filename + ".mp4")
	}
	p := filepath.Join(dest, filename)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

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

	if audioOnly != "" {
		err := s.downloadAudioOnly(ctx, video, videoFmt, audioFmt, title, dest, owner, paths, limiter, skipChecksums, audioOnly, onProgress)
		return paths.Load(owner), err
	}
	if audioFmt == nil {
		err := s.downloadStreamDirect(ctx, video, videoFmt, title, dest, owner, paths, limiter, skipChecksums, onProgress)
		return paths.Load(owner), err
	}
	err = s.downloadAndMux(ctx, video, videoFmt, audioFmt, title, dest, owner, paths, limiter, skipChecksums, onProgress)
	return paths.Load(owner), err
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
	finalPath = strings.TrimSuffix(finalPath, filepath.Ext(finalPath)) + "." + ext
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
	tempPath := partOutputPath(finalPath)
	defer os.Remove(tempPath)

	streamTemp := tempPath + ".stream"
	defer os.Remove(streamTemp)

	if _, err := s.downloadStreamToFileProgress(ctx, video, srcFormat, streamTemp, limiter, func(n, total int64) {
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

	checksum, err := s.downloadStreamToFileProgress(ctx, video, format, tempPath, limiter, onProgress)
	if err != nil {
		return err
	}
	return commitOutputWithChecksum(tempPath, finalPath, skipChecksums, checksum)
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

	outputPath := paths.Claim(safeOutputPath(dest, title), owner)
	skip, err := prepareOutput(outputPath, skipChecksums)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	tmpDir, err := newMuxWorkDir(dest)
	if err != nil {
		return model.NewNonRetryable(err)
	}
	defer os.RemoveAll(tmpDir)

	videoPath := filepath.Join(tmpDir, "video."+videoExt)
	audioPath := filepath.Join(tmpDir, "audio."+audioExt)
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
	if err := streamPairErr(videoErr, audioErr); err != nil {
		return err
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

func (s *Scraper) downloadStreamToFileProgress(ctx context.Context, video *ytdl.Video, format *ytdl.Format, path string, limiter *rate.Limiter, onProgress func(int64, int64)) (string, error) {
	s.videoMu.Lock()
	stream, size, err := s.video.GetStreamContext(ctx, video, format)
	s.videoMu.Unlock()
	if err != nil {
		return "", err
	}
	defer stream.Close()
	return copyStreamToFile(ctx, stream, size, path, limiter, onProgress)
}

// copyStreamToFile writes stream to path, reporting progress along the way.
// When size > 0 the stream must deliver exactly that many bytes: a stream
// that ends early (io.EOF before size bytes) is rejected as truncated so a
// partial file can never be committed and blessed with a checksum sidecar.
func copyStreamToFile(ctx context.Context, stream io.Reader, size int64, path string, limiter *rate.Limiter, onProgress func(int64, int64)) (string, error) {
	f, err := fileutil.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
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

	var read int64
	hasher := sha256.New()
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
	return commitOutputWithChecksum(tempPath, finalPath, skipChecksums, "")
}

func commitOutputWithChecksum(tempPath, finalPath string, skipChecksums bool, checksum string) error {
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
	return nil
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

// partOutputPath derives the temp file for ffmpeg from the final path,
// keeping the real extension so ffmpeg can infer the output muxer. A bare
// ".part" suffix matches no muxer and makes ffmpeg abort.
func partOutputPath(finalPath string) string {
	return finalPath + ".part" + filepath.Ext(finalPath)
}

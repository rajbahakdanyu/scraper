// internal/parser/youtube/youtube.go
package youtube

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/user/scraper/internal/model"
)

// ProgressFunc is called with cumulative bytes downloaded.
type ProgressFunc = func(bytes int64, total int64)

type Scraper struct {
	video    *ytdl.Client
	progress func(int64, int64)
}

// NewYouTubeScraper creates a YouTube scraper using the kkdai/youtube library
// for real stream URL extraction and downloads.
func NewYouTubeScraper() *Scraper {
	return &Scraper{
		video: &ytdl.Client{},
	}
}

// Compile-time checks that Scraper satisfies the duck-typed interfaces that
// cmd/root.go and internal/ui/dashboard rely on via runtime type assertion
// (rather than a declared Go interface, since cmd depends on model.Scraper
// generically and only needs these extra YouTube-specific methods for the
// "youtube" site). If any of these signatures ever drift, the build breaks
// here instead of the assertions silently failing at runtime.
var (
	_ interface {
		DownloadVideoWithQuality(videoID, dest string, height int) error
	} = (*Scraper)(nil)
	_ interface{ ExtractVideoID(string) string }       = (*Scraper)(nil)
	_ interface{ SetProgressFunc(func(int64, int64)) } = (*Scraper)(nil)
)

// SetProgressFunc sets a callback for download progress reporting.
func (s *Scraper) SetProgressFunc(fn func(int64, int64)) {
	s.progress = fn
}

func (s *Scraper) Name() string {
	return "youtube"
}

// Parse extracts video metadata and returns one MediaItem per available
// quality level (deduplicated by resolution). Each item's Title is labeled
// with the quality (e.g. "Video Title [1080p]").
func (s *Scraper) Parse(rawURL string) ([]model.MediaItem, error) {
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

	// Group formats by resolution, pick best codec per resolution.
	byHeight := map[int]ytdl.Format{}
	for _, f := range video.Formats {
		if f.Height == 0 {
			continue
		}
		existing, ok := byHeight[f.Height]
		if !ok || f.Bitrate > existing.Bitrate {
			byHeight[f.Height] = f
		}
	}

	// Sort resolutions descending.
	heights := make([]int, 0, len(byHeight))
	for h := range byHeight {
		heights = append(heights, h)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(heights)))

	if len(heights) == 0 {
		return nil, fmt.Errorf("youtube: no video formats found for %s", videoID)
	}

	var items []model.MediaItem
	for _, h := range heights {
		f := byHeight[h]
		streamURL, err := s.video.GetStreamURL(video, &f)
		if err != nil {
			continue
		}
		ext := "mp4"
		if strings.Contains(f.MimeType, "webm") {
			ext = "webm"
		}
		_ = ext
		label := f.QualityLabel
		if label == "" {
			label = fmt.Sprintf("%dp", h)
		}

		note := "video-only (will mux audio)"
		if f.AudioChannels > 0 {
			note = "video+audio"
		}

		items = append(items, model.MediaItem{
			URL:       streamURL,
			Title:     fmt.Sprintf("%s [%s] (%s)", title, label, note),
			Size:      f.ContentLength,
			MimeType:  f.MimeType,
			Type:      model.MediaTypeVideo,
			Thumbnail: fmt.Sprintf("%d", f.ItagNo),
		})
	}

	return items, nil
}

// DownloadVideo downloads a YouTube video at the given quality. For muxed
// formats (itag 18, 360p), it downloads directly. For video-only formats,
// it downloads video + audio separately and muxes with ffmpeg.
func (s *Scraper) DownloadVideo(videoID, dest string) error {
	return s.DownloadVideoWithQuality(videoID, dest, 0)
}

// DownloadVideoWithQuality downloads a YouTube video at a specific height.
// If height is 0, picks the best available.
func (s *Scraper) DownloadVideoWithQuality(videoID, dest string, height int) error {
	video, err := s.video.GetVideoContext(context.Background(), videoID)
	if err != nil {
		return fmt.Errorf("youtube: failed to fetch video info: %w", err)
	}

	title := video.Title
	if title == "" {
		title = videoID
	}

	// Pick video format for the requested quality.
	var videoFormat *ytdl.Format
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.Height == 0 {
			continue
		}
		if height > 0 && f.Height != height {
			continue
		}
		if videoFormat == nil || f.Height > videoFormat.Height ||
			(f.Height == videoFormat.Height && f.Bitrate > videoFormat.Bitrate) {
			videoFormat = f
		}
	}
	if videoFormat == nil {
		return fmt.Errorf("youtube: no video format at %dp", height)
	}

	// If muxed (has audio), download directly.
	if videoFormat.AudioChannels > 0 {
		return s.downloadStreamDirect(video, videoFormat, title, dest)
	}

	// Pick best audio format.
	var audioFormat *ytdl.Format
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.AudioChannels == 0 {
			continue
		}
		if audioFormat == nil || f.Bitrate > audioFormat.Bitrate {
			audioFormat = f
		}
	}
	if audioFormat == nil {
		return s.downloadStreamDirect(video, videoFormat, title, dest)
	}

	return s.downloadAndMux(video, videoFormat, audioFormat, title, dest)
}

func (s *Scraper) downloadStreamDirect(video *ytdl.Video, format *ytdl.Format, title, dest string) error {
	ext := "mp4"
	if strings.Contains(format.MimeType, "webm") {
		ext = "webm"
	}
	path := filepath.Join(dest, sanitizeFilename(title)+"."+ext)

	stream, _, err := s.video.GetStream(video, format)
	if err != nil {
		return fmt.Errorf("youtube: failed to open stream: %w", err)
	}
	defer stream.Close()

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, stream)
	return err
}

func (s *Scraper) downloadAndMux(video *ytdl.Video, videoFmt, audioFmt *ytdl.Format, title, dest string) error {
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
	outputPath := filepath.Join(dest, sanitizeFilename(title)+".mp4")

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

	// Download video stream.
	if err := s.downloadStreamToFileProgress(video, videoFmt, videoPath, func(n int64, total int64) {
		videoRead = n
		s.reportProgress(videoRead+audioRead, combinedTotal)
	}); err != nil {
		return fmt.Errorf("youtube: failed to download video: %w", err)
	}

	// Download audio stream.
	if err := s.downloadStreamToFileProgress(video, audioFmt, audioPath, func(n int64, total int64) {
		audioRead = n
		s.reportProgress(videoRead+audioRead, combinedTotal)
	}); err != nil {
		return fmt.Errorf("youtube: failed to download audio: %w", err)
	}

	// Mux with ffmpeg — report as nearly complete while muxing.
	s.reportProgress(combinedTotal-1, combinedTotal)
	cmd := exec.Command("ffmpeg", "-y",
		"-i", videoPath,
		"-i", audioPath,
		"-c:v", "copy",
		"-c:a", "aac",
		"-movflags", "+faststart",
		outputPath,
	)
	cmd.Stderr = nil
	cmd.Stdout = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("youtube: ffmpeg mux failed: %w", err)
	}

	s.reportProgress(combinedTotal, combinedTotal)
	return nil
}

func (s *Scraper) reportProgress(bytes, total int64) {
	if s.progress != nil {
		s.progress(bytes, total)
	}
}

func (s *Scraper) downloadStreamToFileProgress(video *ytdl.Video, format *ytdl.Format, path string, onProgress func(int64, int64)) error {
	stream, size, err := s.video.GetStream(video, format)
	if err != nil {
		return err
	}
	defer stream.Close()

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	var read int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := stream.Read(buf)
		if n > 0 {
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

// internal/parser/youtube/youtube.go
package youtube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/user/scraper/internal/model"
)

type Scraper struct {
	client  *http.Client
	video   *ytdl.Client
	baseURL string
}

// NewYouTubeScraper creates a YouTube scraper using the kkdai/youtube library
// for real stream URL extraction and downloads.
func NewYouTubeScraper() *Scraper {
	return &Scraper{
		client:  &http.Client{},
		video:   &ytdl.Client{},
		baseURL: "https://www.youtube.com",
	}
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

	// Download video stream.
	fmt.Printf("  Downloading video stream (%dp)...\n", videoFmt.Height)
	if err := s.downloadStreamToFile(video, videoFmt, videoPath); err != nil {
		return fmt.Errorf("youtube: failed to download video: %w", err)
	}

	// Download audio stream.
	fmt.Printf("  Downloading audio stream...\n")
	if err := s.downloadStreamToFile(video, audioFmt, audioPath); err != nil {
		return fmt.Errorf("youtube: failed to download audio: %w", err)
	}

	// Mux with ffmpeg.
	fmt.Printf("  Muxing video+audio with ffmpeg...\n")
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

	fmt.Printf("  Saved: %s\n", outputPath)
	return nil
}

func (s *Scraper) downloadStreamToFile(video *ytdl.Video, format *ytdl.Format, path string) error {
	stream, _, err := s.video.GetStream(video, format)
	if err != nil {
		return err
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

func sanitizeFilename(s string) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 32 && r != ':' && r != '<' && r != '>' && r != '|' && r != '/' && r != '\\' && r != '?' && r != '*' {
			clean = append(clean, r)
		}
	}
	return string(clean)
}

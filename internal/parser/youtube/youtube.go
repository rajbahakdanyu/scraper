// internal/parser/youtube/youtube.go
package youtube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

// Parse extracts video metadata and stream formats. The first MediaItem
// contains the best-quality combined video+audio stream URL.
func (s *Scraper) Parse(rawURL string) ([]model.MediaItem, error) {
	videoID := s.extractVideoID(rawURL)
	if videoID == "" {
		return nil, fmt.Errorf("youtube: could not extract video ID from URL")
	}

	video, err := s.video.GetVideoContext(context.Background(), videoID)
	if err != nil {
		return nil, fmt.Errorf("youtube: failed to fetch video info: %w", err)
	}

	formats := video.Formats.WithAudioChannels()
	if len(formats) == 0 {
		return nil, fmt.Errorf("youtube: no downloadable stream formats found for %s", videoID)
	}

	best := formats[0]
	url, err := s.video.GetStreamURL(video, &best)
	if err != nil {
		return nil, fmt.Errorf("youtube: failed to get stream URL: %w", err)
	}

	title := video.Title
	if title == "" {
		title = videoID
	}
	ext := "mp4"
	if best.MimeType != "" && strings.Contains(best.MimeType, "webm") {
		ext = "webm"
	}

	return []model.MediaItem{
		{
			URL:      url,
			Title:    title + "." + ext,
			Size:     best.ContentLength,
			MimeType: best.MimeType,
			Type:     model.MediaTypeVideo,
		},
	}, nil
}

// DownloadVideo downloads a YouTube video directly using the kkdai/youtube
// library, which handles YouTube's signature deciphering internally. This
// is used as an alternative to the generic HTTP downloader for YouTube
// URLs that require signed stream access.
func (s *Scraper) DownloadVideo(videoID, dest string) error {
	video, err := s.video.GetVideoContext(context.Background(), videoID)
	if err != nil {
		return fmt.Errorf("youtube: failed to fetch video info: %w", err)
	}

	formats := video.Formats.WithAudioChannels()
	if len(formats) == 0 {
		return fmt.Errorf("youtube: no downloadable formats")
	}

	best := formats[0]

	title := video.Title
	if title == "" {
		title = videoID
	}
	ext := "mp4"
	if best.MimeType != "" && strings.Contains(best.MimeType, "webm") {
		ext = "webm"
	}

	filename := sanitizeFilename(title + "." + ext)
	path := filepath.Join(dest, filename)

	resp, size, err := s.video.GetStream(video, &best)
	if err != nil {
		return fmt.Errorf("youtube: failed to open stream: %w", err)
	}
	defer resp.Close()

	_ = size

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp)
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

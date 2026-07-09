// internal/parser/youtube/youtube.go
package youtube

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/user/scraper/internal/model"
)

// ErrStreamExtractionUnsupported is returned by Parse because real YouTube
// stream URL extraction requires signature deciphering and format
// negotiation, which is out of scope for this project. The scraper still
// fetches metadata (title) via the public oEmbed endpoint.
var ErrStreamExtractionUnsupported = errors.New(
	"youtube: real stream URL extraction is not implemented; " +
		"this scraper fetches metadata only via the oEmbed endpoint. " +
		"Use yt-dlp or similar for actual downloads",
)

type Scraper struct {
	client  *http.Client
	baseURL string
}

// NewYouTubeScraper creates a YouTube scraper using the default HTTP client.
func NewYouTubeScraper() *Scraper {
	return &Scraper{
		client:  &http.Client{},
		baseURL: "https://www.youtube.com",
	}
}

// newYouTubeScraperWithBase is used by tests to point at a mock server.
func newYouTubeScraperWithBase(baseURL string) *Scraper {
	return &Scraper{
		client:  &http.Client{},
		baseURL: baseURL,
	}
}

func (s *Scraper) Name() string {
	return "youtube"
}

// Parse fetches video metadata via the oEmbed endpoint. It returns the
// metadata as a MediaItem for display purposes, but the URL field is left
// empty because real stream extraction is not supported.
func (s *Scraper) Parse(rawURL string) ([]model.MediaItem, error) {
	videoID := s.extractVideoID(rawURL)
	if videoID == "" {
		return nil, errors.New("youtube: could not extract video ID from URL")
	}

	oembedURL := s.baseURL + "/oembed?url=" + url.QueryEscape(rawURL) + "&format=json"
	resp, err := s.client.Get(oembedURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info struct {
		Title      string `json:"title"`
		AuthorName string `json:"author_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return []model.MediaItem{
		{
			URL:   "",
			Title: info.Title,
			Type:  model.MediaTypeVideo,
		},
	}, ErrStreamExtractionUnsupported
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

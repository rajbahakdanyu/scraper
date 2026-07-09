// internal/parser/youtube/youtube.go
package youtube

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/user/scraper/internal/model"
)

type Scraper struct {
	client *http.Client
}

func NewYouTubeScraper() *Scraper {
	return &Scraper{
		client: &http.Client{},
	}
}

func (s *Scraper) Name() string {
	return "youtube"
}

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	// Extract video ID
	_ = s.extractVideoID(url)
	
	// Fetch video info (simplified - real implementation would use YouTube API or scraping)
	resp, err := s.client.Get("https://www.youtube.com/oembed?url=" + url + "&format=json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	// Return video(s) - real implementation would fetch stream URLs
	return []model.MediaItem{
		{
			URL:      "https://example.com/video.mp4",
			Title:    info.Title,
			MimeType: "video/mp4",
			Type:     model.MediaTypeVideo,
		},
	}, nil
}

func (s *Scraper) extractVideoID(url string) string {
	if strings.Contains(url, "v=") {
		parts := strings.Split(url, "v=")
		if len(parts) < 2 {
			return ""
		}
		return strings.Split(parts[1], "&")[0]
	}
	if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		if len(parts) < 2 {
			return ""
		}
		return parts[1]
	}
	return ""
}
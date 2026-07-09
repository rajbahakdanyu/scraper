// internal/parser/gofile/gofile.go
package gofile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/user/scraper/internal/model"
)

type Scraper struct {
	client *http.Client
}

func NewGoFileScraper() *Scraper {
	return &Scraper{client: &http.Client{}}
}

func (s *Scraper) Name() string {
	return "gofile"
}

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	contentID := extractContentID(url)
	if contentID == "" {
		return nil, fmt.Errorf("could not extract content ID from URL: %s", url)
	}

	apiURL := "https://api.gofile.io/contents/" + contentID
	resp, err := s.client.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Children map[string]struct {
				Name string `json:"name"`
				Link string `json:"link"`
				Size int64  `json:"size"`
			} `json:"children"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Status != "ok" {
		return nil, fmt.Errorf("gofile API error: %s", result.Status)
	}

	var items []model.MediaItem
	for _, child := range result.Data.Children {
		if child.Name == "" || child.Link == "" {
			continue
		}

		mediaType := model.MediaTypeVideo
		if strings.HasSuffix(strings.ToLower(child.Name), ".jpg") ||
			strings.HasSuffix(strings.ToLower(child.Name), ".jpeg") ||
			strings.HasSuffix(strings.ToLower(child.Name), ".png") ||
			strings.HasSuffix(strings.ToLower(child.Name), ".gif") {
			mediaType = model.MediaTypeImage
		}

		items = append(items, model.MediaItem{
			URL:      child.Link,
			Title:    child.Name,
			Size:     child.Size,
			MimeType: "application/octet-stream",
			Type:     mediaType,
		})
	}

	return items, nil
}

func extractContentID(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}
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
	client    *http.Client
	baseURL   string
	userAgent string
}

func NewGoFileScraper() *Scraper {
	return &Scraper{
		client:    &http.Client{},
		baseURL:   "https://api.gofile.io/contents/",
		userAgent: "Scraper/1.0",
	}
}

// NewGoFileScraperWithConfig creates a scraper with a custom user agent.
func NewGoFileScraperWithConfig(userAgent string) *Scraper {
	if userAgent == "" {
		userAgent = "Scraper/1.0"
	}
	return &Scraper{
		client:    &http.Client{},
		baseURL:   "https://api.gofile.io/contents/",
		userAgent: userAgent,
	}
}

func (s *Scraper) Name() string {
	return "gofile"
}

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	contentID := extractContentID(url)
	if contentID == "" {
		return nil, fmt.Errorf("could not extract content ID from URL: %s", url)
	}

	apiURL := s.baseURL + contentID
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
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

		items = append(items, model.MediaItem{
			URL:      child.Link,
			Title:    child.Name,
			Size:     child.Size,
			MimeType: "application/octet-stream",
			Type:     model.MediaTypeForName(child.Name),
		})
	}

	return items, nil
}

func extractContentID(rawURL string) string {
	for _, prefix := range []string{"/s/", "/d/", "/v/", "/c/"} {
		if idx := strings.Index(rawURL, prefix); idx >= 0 {
			rest := rawURL[idx+len(prefix):]
			return strings.SplitN(rest, "/", 2)[0]
		}
	}
	parts := strings.Split(rawURL, "/")
	if len(parts) < 2 {
		return ""
	}
	last := parts[len(parts)-1]
	if last != "" {
		return last
	}
	return parts[len(parts)-2]
}

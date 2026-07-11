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
	token     string
}

func NewGoFileScraper() *Scraper {
	return &Scraper{
		client:    &http.Client{},
		baseURL:   "https://api.gofile.io",
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
	}
}

// NewGoFileScraperWithConfig creates a scraper with a custom user agent.
func NewGoFileScraperWithConfig(userAgent string) *Scraper {
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
	}
	return &Scraper{
		client:    &http.Client{},
		baseURL:   "https://api.gofile.io",
		userAgent: userAgent,
	}
}

func (s *Scraper) Name() string {
	return "gofile"
}

// getGuestToken creates a guest account and stores the token for API calls.
func (s *Scraper) getGuestToken() error {
	if s.token != "" {
		return nil
	}

	req, err := http.NewRequest("POST", s.baseURL+"/accounts", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if result.Status != "ok" || result.Data.Token == "" {
		return fmt.Errorf("gofile: failed to create guest account: %s", result.Status)
	}

	s.token = result.Data.Token
	return nil
}

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	contentID := extractContentID(url)
	if contentID == "" {
		return nil, fmt.Errorf("could not extract content ID from URL: %s", url)
	}

	if err := s.getGuestToken(); err != nil {
		return nil, err
	}

	apiURL := s.baseURL + "/contents/" + contentID
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Name     string `json:"name"`
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
		if result.Status == "error-notPremium" {
			return nil, fmt.Errorf("gofile: this content requires a premium account — set a GoFile token via --config (user_agent field) or use a different link")
		}
		if result.Status == "error-token" {
			// Token may have expired; retry once
			s.token = ""
			if err := s.getGuestToken(); err != nil {
				return nil, err
			}
			return s.Parse(url)
		}
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

	if len(items) == 0 {
		return nil, fmt.Errorf("gofile: no downloadable files found for content ID %s", contentID)
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

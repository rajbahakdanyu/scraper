// internal/parser/goonbox/goonbox.go
package goonbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

const apiBase = "https://goonbox.cr/api"

type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
}

func NewGoonBoxScraper() *Scraper {
	return &Scraper{
		client:    netutil.NewPublicClient(30 * time.Second),
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   apiBase,
	}
}

func (s *Scraper) Name() string {
	return "goonbox"
}

func (s *Scraper) Parse(albumURL string) ([]model.MediaItem, error) {
	albumID := extractAlbumID(albumURL)
	if albumID == "" {
		return nil, fmt.Errorf("goonbox: could not extract album ID from URL: %s", albumURL)
	}

	var items []model.MediaItem
	var unsafeErrs []error
	page := 1

	for {
		url := fmt.Sprintf("%s/albums/%s?page=%d", s.baseURL, albumID, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("goonbox: build album request: %w", err)
		}
		req.Header.Set("User-Agent", s.userAgent)

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			if err := resp.Body.Close(); err != nil {
				return nil, fmt.Errorf("goonbox: close response body: %w", err)
			}
			return nil, fmt.Errorf("goonbox: HTTP %d for album %s page %d", resp.StatusCode, albumID, page)
		}

		var result struct {
			Album struct {
				Title string `json:"title"`
			} `json:"album"`
			Images []struct {
				OriginalURL      string `json:"original_url"`
				OriginalFilename string `json:"original_filename"`
				SizeBytes        int64  `json:"size_bytes"`
				Mime             string `json:"mime"`
			} `json:"images"`
			Pagination struct {
				CurrentPage int `json:"current_page"`
				LastPage    int `json:"last_page"`
			} `json:"pagination"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, errors.Join(err, resp.Body.Close())
		}
		if err := resp.Body.Close(); err != nil {
			return nil, fmt.Errorf("goonbox: close response body: %w", err)
		}

		for _, img := range result.Images {
			if img.OriginalURL == "" {
				continue
			}
			if err := netutil.NewPolicy().ValidateURL(img.OriginalURL); err != nil {
				unsafeErrs = append(unsafeErrs, fmt.Errorf("unsafe image URL: %w", err))
				continue
			}
			title := img.OriginalFilename
			if title == "" {
				title = fmt.Sprintf("goonbox_%d", len(items)+1)
			}
			items = append(items, model.MediaItem{
				URL:      img.OriginalURL,
				Title:    title,
				Size:     img.SizeBytes,
				MimeType: img.Mime,
				Type:     model.MediaTypeForName(title),
			})
		}

		if page >= result.Pagination.LastPage {
			break
		}
		page++
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("goonbox: no images found in album %s", albumID)
	}

	return items, model.NewPartialError("goonbox album", unsafeErrs)
}

func extractAlbumID(rawURL string) string {
	if idx := strings.IndexByte(rawURL, '?'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	for i, p := range parts {
		if p == "a" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	last := parts[len(parts)-1]
	if last != "" && last != "a" {
		return last
	}
	return ""
}

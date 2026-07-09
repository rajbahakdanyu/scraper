// internal/parser/bunker/bunker.go
package bunker

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/user/scraper/internal/model"
)

type Scraper struct {
	client    *http.Client
	userAgent string
}

// NewBunkerScraper creates a new Bunker scraper using the default HTTP client.
func NewBunkerScraper() *Scraper {
	return &Scraper{client: &http.Client{}, userAgent: "Scraper/1.0"}
}

// NewBunkerScraperWithConfig creates a scraper with a custom user agent.
func NewBunkerScraperWithConfig(userAgent string) *Scraper {
	if userAgent == "" {
		userAgent = "Scraper/1.0"
	}
	return &Scraper{client: &http.Client{}, userAgent: userAgent}
}

// Name returns the logical scraper name used for CLI dispatch.
func (s *Scraper) Name() string {
	return "bunker"
}

// Parse fetches a Bunker gallery page and extracts its media items.
func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bunker: unexpected status %d for %s", resp.StatusCode, url)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var items []model.MediaItem
	doc.Find("a.link").Each(func(i int, sel *goquery.Selection) {
		href, ok := sel.Attr("href")
		if !ok || href == "" {
			return
		}

		title := strings.TrimSpace(sel.Text())
		if title == "" {
			title = path.Base(href)
		}

		items = append(items, model.MediaItem{
			URL:      href,
			Title:    title,
			MimeType: "application/octet-stream",
			Type:     model.MediaTypeForName(title),
		})
	})

	return items, nil
}

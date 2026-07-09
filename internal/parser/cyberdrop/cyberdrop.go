package cyberdrop
// internal/parser/cyberdrop/cyberdrop.go
package cyberdrop

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/user/scraper/internal/model"
)

// Scraper parses CyberDrop gallery pages for downloadable media links.
type Scraper struct {
	client *http.Client
}

// NewCyberDropScraper creates a new CyberDrop scraper using the default HTTP client.
func NewCyberDropScraper() *Scraper {
	return &Scraper{client: &http.Client{}}
}

// Name returns the logical scraper name used for CLI dispatch.
func (s *Scraper) Name() string {
	return "cyberdrop"
}

// Parse fetches a CyberDrop gallery page and extracts its media items.
func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cyberdrop: unexpected status %d for %s", resp.StatusCode, url)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var items []model.MediaItem
	doc.Find("a.image-link").Each(func(i int, sel *goquery.Selection) {
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
			Type:     mediaTypeForName(title),
		})
	})

	return items, nil
}

// mediaTypeForName infers the media type from a filename's extension.
func mediaTypeForName(name string) model.MediaType {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"),
		strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".gif"),
		strings.HasSuffix(lower, ".webp"):
		return model.MediaTypeImage
	default:
		return model.MediaTypeVideo
	}
}

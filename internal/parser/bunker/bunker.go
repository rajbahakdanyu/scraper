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
	baseURL   string
}

// NewBunkerScraper creates a new Bunker scraper using the default HTTP client.
func NewBunkerScraper() *Scraper {
	return &Scraper{
		client:    &http.Client{},
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   "https://bunkr.cr",
	}
}

// NewBunkerScraperWithConfig creates a scraper with a custom user agent.
func NewBunkerScraperWithConfig(userAgent string) *Scraper {
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
	}
	return &Scraper{client: &http.Client{}, userAgent: userAgent, baseURL: "https://bunkr.cr"}
}

func (s *Scraper) Name() string {
	return "bunker"
}

// Parse fetches a Bunker album page, extracts file links, then visits each
// file page to get the direct download URL and filename.
func (s *Scraper) Parse(albumURL string) ([]model.MediaItem, error) {
	doc, err := s.fetchDoc(albumURL)
	if err != nil {
		return nil, err
	}

	var fileLinks []string
	doc.Find(`a[aria-label="download"]`).Each(func(_ int, sel *goquery.Selection) {
		href, ok := sel.Attr("href")
		if !ok || href == "" {
			return
		}
		fullURL := s.resolveURL(href)
		fileLinks = append(fileLinks, fullURL)
	})

	if len(fileLinks) == 0 {
		return nil, fmt.Errorf("bunker: no file links found on album page")
	}

	var items []model.MediaItem
	for _, fileURL := range fileLinks {
		item, err := s.parseFilePage(fileURL)
		if err != nil {
			continue
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("bunker: could not extract any download URLs")
	}

	return items, nil
}

// parseFilePage visits a /f/<id> page and extracts the direct download link
// and filename from the page's title and dl.bunkr.cr link.
func (s *Scraper) parseFilePage(fileURL string) (model.MediaItem, error) {
	doc, err := s.fetchDoc(fileURL)
	if err != nil {
		return model.MediaItem{}, err
	}

	title := doc.Find("title").Text()
	title = strings.TrimSpace(title)
	// Title format: "Filename.ext | Bunkr"
	if idx := strings.LastIndex(title, " | Bunkr"); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}

	var downloadURL string
	doc.Find(`a[href*="dl.bunkr"]`).Each(func(_ int, sel *goquery.Selection) {
		if downloadURL == "" {
			href, ok := sel.Attr("href")
			if ok && href != "" {
				downloadURL = href
			}
		}
	})

	if downloadURL == "" {
		return model.MediaItem{}, fmt.Errorf("no download link found on %s", fileURL)
	}

	if title == "" {
		title = path.Base(downloadURL)
	}

	return model.MediaItem{
		URL:      downloadURL,
		Title:    title,
		MimeType: "application/octet-stream",
		Type:     model.MediaTypeForName(title),
	}, nil
}

func (s *Scraper) fetchDoc(url string) (*goquery.Document, error) {
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
		return nil, fmt.Errorf("bunker: HTTP %d for %s", resp.StatusCode, url)
	}

	return goquery.NewDocumentFromReader(resp.Body)
}

func (s *Scraper) resolveURL(href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	return s.baseURL + href
}

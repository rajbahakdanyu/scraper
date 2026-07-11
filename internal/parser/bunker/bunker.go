// internal/parser/bunker/bunker.go
package bunker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

// Parse fetches a Bunker album page, extracts file links, visits each file
// page to get the direct download URL (via the bunkr API + signing service),
// and returns resolved media items.
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

// parseFilePage visits a /f/<id> page, extracts the file ID, then uses the
// bunkr API to get the CDN path and signs the URL for direct download.
func (s *Scraper) parseFilePage(fileURL string) (model.MediaItem, error) {
	doc, err := s.fetchDoc(fileURL)
	if err != nil {
		return model.MediaItem{}, err
	}

	title := strings.TrimSpace(doc.Find("title").Text())
	if idx := strings.LastIndex(title, " | Bunkr"); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}

	// Find the download button with data-id
	dlBtn := doc.Find("#download-btn")
	if dlBtn.Length() == 0 {
		return model.MediaItem{}, fmt.Errorf("no download button on %s", fileURL)
	}
	fileID, _ := dlBtn.Attr("data-id")
	if fileID == "" {
		return model.MediaItem{}, fmt.Errorf("no file ID on %s", fileURL)
	}

	// Extract the ogname from script
	var ogName string
	doc.Find("script").Each(func(_ int, sel *goquery.Selection) {
		text := sel.Text()
		if idx := strings.Index(text, `var ogname = "`); idx >= 0 {
			start := idx + len(`var ogname = "`)
			end := strings.Index(text[start:], `"`)
			if end >= 0 {
				ogName = text[start : start+end]
			}
		}
	})

	if title == "" {
		title = ogName
	}

	// POST to the API to get the media URL
	mediaURL, err := s.getMediaURL(fileURL, fileID)
	if err != nil {
		return model.MediaItem{}, err
	}

	// Add original name as query param
	if title != "" {
		u, _ := url.Parse(mediaURL)
		if u != nil {
			q := u.Query()
			q.Set("n", title)
			u.RawQuery = q.Encode()
			mediaURL = u.String()
		}
	}

	if title == "" {
		title = path.Base(mediaURL)
	}

	return model.MediaItem{
		URL:      mediaURL,
		Title:    title,
		MimeType: "application/octet-stream",
		Type:     model.MediaTypeForName(title),
	}, nil
}

// getMediaURL replicates the bunkr download flow:
// 1. POST to /api/_001_v2 with the file ID to get CDN base + path
// 2. Sign the URL via glb-apisign.cdn.cr/sign
func (s *Scraper) getMediaURL(filePageURL, fileID string) (string, error) {
	// Extract the dl.bunkr.cr origin from the file page URL
	u, err := url.Parse(filePageURL)
	if err != nil {
		return "", err
	}
	apiURL := u.Scheme + "://" + u.Host + "/api/_001_v2"

	// Step 1: POST to get media metadata
	req, err := http.NewRequest("POST", apiURL, strings.NewReader(`{"id":"`+fileID+`"}`))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", filePageURL)
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bunker API returned HTTP %d", resp.StatusCode)
	}

	var meta struct {
		MediaFiles string `json:"mediafiles"`
		Path       string `json:"path"`
		Original   string `json:"original"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", err
	}

	if meta.MediaFiles == "" || meta.Path == "" {
		return "", fmt.Errorf("bunker API returned empty media URL")
	}

	rawURL := meta.MediaFiles + meta.Path

	// Step 2: Sign the URL
	parsedURL, _ := url.Parse(rawURL)
	signedPath := parsedURL.Path

	signReq, _ := http.NewRequest("GET", "https://glb-apisign.cdn.cr/sign?path="+url.QueryEscape(signedPath), nil)
	signReq.Header.Set("User-Agent", s.userAgent)

	signResp, err := s.client.Do(signReq)
	if err != nil {
		// If signing fails, return the unsigned URL as fallback
		return rawURL, nil
	}
	defer signResp.Body.Close()

	if signResp.StatusCode != http.StatusOK {
		return rawURL, nil
	}

	var sign struct {
		Token string `json:"token"`
		Ex    int64  `json:"ex"`
	}
	if err := json.NewDecoder(signResp.Body).Decode(&sign); err != nil {
		return rawURL, nil
	}

	q := parsedURL.Query()
	q.Set("token", sign.Token)
	q.Set("ex", fmt.Sprintf("%d", sign.Ex))
	parsedURL.RawQuery = q.Encode()

	return parsedURL.String(), nil
}

func (s *Scraper) fetchDoc(pageURL string) (*goquery.Document, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
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
		return nil, fmt.Errorf("bunker: HTTP %d for %s", resp.StatusCode, pageURL)
	}

	return goquery.NewDocumentFromReader(resp.Body)
}

func (s *Scraper) resolveURL(href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	return s.baseURL + href
}

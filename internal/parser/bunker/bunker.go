// internal/parser/bunker/bunker.go
package bunker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
	signURL   string
}

// NewBunkerScraper creates a new Bunker scraper using the default HTTP client.
func NewBunkerScraper() *Scraper {
	return &Scraper{
		client:    &http.Client{Timeout: 30 * time.Second},
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   "https://bunkr.cr",
		signURL:   "https://glb-apisign.cdn.cr/sign",
	}
}

func (s *Scraper) Name() string {
	return "bunker"
}

// Parse fetches a Bunker album page, extracts file links, visits each file
// page to get the dl.bunkr.cr link, then uses the bunkr API + signing service
// to resolve the final signed CDN download URL.
func (s *Scraper) Parse(albumURL string) ([]model.MediaItem, error) {
	// Strip query params — ?advanced=1 switches to JS-rendered mode
	// which doesn't have server-side file links in the HTML.
	if idx := strings.Index(albumURL, "?"); idx >= 0 {
		albumURL = albumURL[:idx]
	}

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

	// Fetch file pages concurrently for speed.
	items := make([]model.MediaItem, len(fileLinks))
	errs := make([]error, len(fileLinks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // max 10 concurrent requests

	for i, fileURL := range fileLinks {
		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item, err := s.parseFilePage(u)
			items[idx] = item
			errs[idx] = err
		}(i, fileURL)
	}
	wg.Wait()

	var result []model.MediaItem
	for i := range items {
		if errs[i] == nil {
			result = append(result, items[i])
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("bunker: could not extract any download URLs: %w", model.NewPartialError("bunker album", errs))
	}

	return result, model.NewPartialError("bunker album", errs)
}

// parseFilePage visits a /f/<id> page to get the dl.bunkr.cr download link,
// then resolves the signed CDN URL via the API.
func (s *Scraper) parseFilePage(fileURL string) (model.MediaItem, error) {
	doc, err := s.fetchDoc(fileURL)
	if err != nil {
		return model.MediaItem{}, err
	}

	title := strings.TrimSpace(doc.Find("title").Text())
	if idx := strings.LastIndex(title, " | Bunkr"); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}

	// Find the download link (dl.bunkr.cr link or any /file/ link)
	var dlLink string
	doc.Find(`a`).Each(func(_ int, sel *goquery.Selection) {
		if dlLink != "" {
			return
		}
		href, ok := sel.Attr("href")
		if !ok || href == "" {
			return
		}
		if strings.Contains(href, "dl.bunkr") || strings.Contains(href, "/file/") {
			dlLink = href
		}
	})

	if dlLink == "" {
		return model.MediaItem{}, fmt.Errorf("no download link on %s", fileURL)
	}

	// Extract the numeric file ID from the dl.bunkr.cr URL
	// e.g. https://dl.bunkr.cr/file/61567794
	fileID := path.Base(dlLink)
	if fileID == "" || fileID == "/" {
		return model.MediaItem{}, fmt.Errorf("could not extract file ID from %s", dlLink)
	}

	// Extract the dl.bunkr.cr origin for the API call
	dlURL, _ := url.Parse(dlLink)
	if dlURL == nil {
		return model.MediaItem{}, fmt.Errorf("invalid dl.bunkr URL: %s", dlLink)
	}

	if title == "" {
		title = fileID
	}

	// Use the API to get the signed CDN URL
	mediaURL, err := s.getSignedMediaURL(dlURL, fileID, title)
	if err != nil {
		return model.MediaItem{}, err
	}

	return model.MediaItem{
		URL:      mediaURL,
		Title:    title,
		MimeType: "application/octet-stream",
		Type:     model.MediaTypeForName(title),
	}, nil
}

// getSignedMediaURL replicates the bunkr download flow:
// 1. POST to /api/_001_v2 with the file ID → get CDN base + path
// 2. Sign the URL via glb-apisign.cdn.cr/sign
func (s *Scraper) getSignedMediaURL(dlURL *url.URL, fileID, originalName string) (string, error) {
	apiURL := dlURL.Scheme + "://" + dlURL.Host + "/api/_001_v2"

	// Step 1: POST to get media metadata
	req, err := http.NewRequest("POST", apiURL, strings.NewReader(`{"id":"`+fileID+`"}`))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", dlURL.String())
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", err
	}

	if meta.MediaFiles == "" || meta.Path == "" {
		return "", fmt.Errorf("bunker API returned empty media URL")
	}

	rawURL := meta.MediaFiles + meta.Path
	parsedURL, _ := url.Parse(rawURL)
	if parsedURL == nil {
		return rawURL, nil
	}

	// Step 2: Sign the URL
	signReq, _ := http.NewRequest("GET", s.signURL+"?path="+url.QueryEscape(parsedURL.Path), nil)
	signReq.Header.Set("User-Agent", s.userAgent)

	signResp, err := s.client.Do(signReq)
	if err != nil {
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
	if originalName != "" {
		q.Set("n", originalName)
	}
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

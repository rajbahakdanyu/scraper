// internal/parser/filester/filester.go
package filester

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"

	"github.com/user/scraper/internal/model"
)

const defaultBaseURL = "https://filester.sh"

// defaultCDNURLs mirrors the CDN_URLS list filester's own /js/file_dl.js
// picks a random entry from when a download token response doesn't include
// its own "server" field.
var defaultCDNURLs = []string{
	"https://cn1.filester.me",
	"https://p1.filester.me",
	"https://rs2.filester.me",
}

// Scraper parses filester.sh/.me folder (/f/<id>) and single-file (/d/<slug>)
// share links.
type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
	cdnURLs   []string
}

// NewFilesterScraper creates a new Filester scraper using the default HTTP client.
func NewFilesterScraper() *Scraper {
	return &Scraper{
		client:    &http.Client{},
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   defaultBaseURL,
		cdnURLs:   defaultCDNURLs,
	}
}

// NewFilesterScraperWithConfig creates a scraper with a custom user agent.
func NewFilesterScraperWithConfig(userAgent string) *Scraper {
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
	}
	return &Scraper{
		client:    &http.Client{},
		userAgent: userAgent,
		baseURL:   defaultBaseURL,
		cdnURLs:   defaultCDNURLs,
	}
}

func (s *Scraper) Name() string {
	return "filester"
}

// Parse handles both folder URLs (/f/<id>) and single-file URLs (/d/<slug>).
func (s *Scraper) Parse(rawURL string) ([]model.MediaItem, error) {
	kind, id := extractKindAndID(rawURL)
	switch kind {
	case "d":
		item, err := s.resolveFile(id, "")
		if err != nil {
			return nil, err
		}
		return []model.MediaItem{item}, nil
	case "f":
		return s.parseFolder(id)
	default:
		return nil, fmt.Errorf("filester: unrecognized URL format: %s", rawURL)
	}
}

// extractKindAndID pulls the "f" or "d" path segment and the ID/slug that
// follows it from a filester URL, ignoring any query string.
func extractKindAndID(rawURL string) (kind, id string) {
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	trimmed := strings.TrimRight(rawURL, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return "", ""
	}
	id = parts[len(parts)-1]
	kind = parts[len(parts)-2]
	if id == "" || (kind != "f" && kind != "d") {
		return "", ""
	}
	return kind, id
}

type folderEntry struct {
	slug string
	name string
	size int64
}

var onclickSlugRe = regexp.MustCompile(`/d/([^'"]+)`)

// parseFolder fetches every page of a folder listing, then resolves each
// file's real download URL concurrently.
func (s *Scraper) parseFolder(id string) ([]model.MediaItem, error) {
	var allEntries []folderEntry
	total := 1

	for page := 1; page <= total; page++ {
		doc, err := s.fetchDoc(fmt.Sprintf("%s/f/%s?page=%d", s.baseURL, id, page))
		if err != nil {
			return nil, err
		}

		if page == 1 {
			if v, ok := doc.Find("#loadAllPagesBtn").Attr("data-total"); ok {
				if n, err := strconv.Atoi(v); err == nil && n > total {
					total = n
				}
			}
		}

		entries := extractFileItems(doc)
		if len(entries) == 0 {
			break
		}
		allEntries = append(allEntries, entries...)
	}

	if len(allEntries) == 0 {
		return nil, fmt.Errorf("filester: no files found in folder %s", id)
	}

	// Resolve download URLs concurrently (bounded), mirroring the bunker
	// scraper's fetch-file-pages-concurrently approach.
	items := make([]model.MediaItem, len(allEntries))
	errs := make([]error, len(allEntries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for i, e := range allEntries {
		wg.Add(1)
		go func(idx int, entry folderEntry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item, err := s.resolveFile(entry.slug, entry.name)
			if err == nil {
				item.Size = entry.size
			}
			items[idx] = item
			errs[idx] = err
		}(i, e)
	}
	wg.Wait()

	var result []model.MediaItem
	for i := range items {
		if errs[i] == nil {
			result = append(result, items[i])
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("filester: could not resolve any download URLs in folder %s", id)
	}
	return result, nil
}

// extractFileItems reads the .file-item divs from a rendered folder page,
// e.g. <div class="file-item" data-name="a.mp4" data-size="123"
// onclick="window.location.href='/d/SLUG'">.
func extractFileItems(doc *goquery.Document) []folderEntry {
	var entries []folderEntry
	doc.Find(".file-item").Each(func(_ int, sel *goquery.Selection) {
		name, ok := sel.Attr("data-name")
		if !ok || name == "" {
			return
		}
		onclick, _ := sel.Attr("onclick")
		m := onclickSlugRe.FindStringSubmatch(onclick)
		if len(m) < 2 || m[1] == "" {
			return
		}
		var size int64
		if sizeStr, ok := sel.Attr("data-size"); ok {
			size, _ = strconv.ParseInt(sizeStr, 10, 64)
		}
		entries = append(entries, folderEntry{slug: m[1], name: name, size: size})
	})
	return entries
}

// resolveFile exchanges a file's slug for a signed, directly-downloadable
// CDN URL via filester's public download-token endpoint
// (POST /v2/api/public/download {"file_slug": slug} ->
// {file, token, server?, name?}), replicating /js/file_dl.js's
// generatePublicDownloadToken() + buildMediaUrl(tokenData, true).
func (s *Scraper) resolveFile(slug, knownName string) (model.MediaItem, error) {
	body, err := json.Marshal(map[string]string{"file_slug": slug})
	if err != nil {
		return model.MediaItem{}, err
	}

	req, err := http.NewRequest("POST", s.baseURL+"/v2/api/public/download", bytes.NewReader(body))
	if err != nil {
		return model.MediaItem{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return model.MediaItem{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return model.MediaItem{}, fmt.Errorf("filester: download token request failed (HTTP %d) for %s", resp.StatusCode, slug)
	}

	var tok struct {
		File   string `json:"file"`
		Token  string `json:"token"`
		Server string `json:"server"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return model.MediaItem{}, err
	}
	if tok.File == "" || tok.Token == "" {
		return model.MediaItem{}, fmt.Errorf("filester: incomplete download token for %s", slug)
	}

	server := tok.Server
	if server == "" {
		if len(s.cdnURLs) == 0 {
			return model.MediaItem{}, fmt.Errorf("filester: no CDN server available for %s", slug)
		}
		server = s.cdnURLs[rand.Intn(len(s.cdnURLs))]
	}

	// Prefer the filename already known from the folder listing (the
	// authoritative original name shown in the UI) over the token
	// response's "name" field, which the site's own JS treats as
	// optional/best-effort and only uses as a download-filename hint.
	title := knownName
	if title == "" {
		title = tok.Name
	}
	if title == "" {
		title = slug
	}

	downloadURL := fmt.Sprintf("%s/v2/%s?token=%s&download=true&n=%s",
		server, tok.File, url.QueryEscape(tok.Token), url.QueryEscape(title))

	return model.MediaItem{
		URL:      downloadURL,
		Title:    title,
		MimeType: "application/octet-stream",
		Type:     model.MediaTypeForName(title),
	}, nil
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
		return nil, fmt.Errorf("filester: HTTP %d for %s", resp.StatusCode, pageURL)
	}

	return goquery.NewDocumentFromReader(resp.Body)
}

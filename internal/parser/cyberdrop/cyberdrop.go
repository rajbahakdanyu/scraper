// internal/parser/cyberdrop/cyberdrop.go
package cyberdrop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/user/scraper/internal/model"
)

const apiBase = "https://api.cyberdrop.cr/api"

type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
}

// NewCyberDropScraper creates a new CyberDrop scraper using the default HTTP client.
func NewCyberDropScraper() *Scraper {
	return &Scraper{
		client:    &http.Client{Timeout: 30 * time.Second},
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   apiBase,
	}
}

func (s *Scraper) Name() string {
	return "cyberdrop"
}

// Parse handles both file URLs (/f/<slug>) and album URLs (/a/<slug>).
// For files: fetches metadata + auth URL from the API.
// For albums: fetches all file links from the album API.
func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	slug := extractSlug(url)
	if slug == "" {
		return nil, fmt.Errorf("cyberdrop: could not extract slug from URL: %s", url)
	}

	if strings.Contains(url, "/a/") || strings.Contains(url, "/album/") {
		return s.parseAlbum(slug)
	}
	return s.parseFile(slug)
}

// parseFile fetches a single file's metadata and resolves the download URL.
func (s *Scraper) parseFile(slug string) ([]model.MediaItem, error) {
	infoURL := fmt.Sprintf("%s/file/info/%s", s.baseURL, slug)

	infoReq, _ := http.NewRequest("GET", infoURL, nil)
	infoReq.Header.Set("User-Agent", s.userAgent)

	infoResp, err := s.client.Do(infoReq)
	if err != nil {
		return nil, err
	}
	defer infoResp.Body.Close()

	if infoResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cyberdrop: file not found (HTTP %d) for slug %s", infoResp.StatusCode, slug)
	}

	var info struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Size    int64  `json:"size"`
		AuthURL string `json:"auth_url"`
	}
	if err := json.NewDecoder(infoResp.Body).Decode(&info); err != nil {
		return nil, err
	}

	if info.AuthURL == "" {
		return nil, fmt.Errorf("cyberdrop: no auth_url returned for %s", slug)
	}

	// Fetch the signed download URL
	authReq, _ := http.NewRequest("GET", info.AuthURL, nil)
	authReq.Header.Set("User-Agent", s.userAgent)

	authResp, err := s.client.Do(authReq)
	if err != nil {
		return nil, err
	}
	defer authResp.Body.Close()

	if authResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cyberdrop: failed to get download URL (HTTP %d)", authResp.StatusCode)
	}

	var auth struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(authResp.Body).Decode(&auth); err != nil {
		return nil, err
	}

	if auth.URL == "" {
		return nil, fmt.Errorf("cyberdrop: empty download URL for %s", slug)
	}

	title := info.Name
	if title == "" {
		title = slug
	}

	return []model.MediaItem{{
		URL:      auth.URL,
		Title:    title,
		Size:     info.Size,
		MimeType: info.Type,
		Type:     model.MediaTypeForName(title),
	}}, nil
}

// parseAlbum fetches all files in an album.
func (s *Scraper) parseAlbum(slug string) ([]model.MediaItem, error) {
	albumURL := fmt.Sprintf("%s/album/%s", s.baseURL, slug)

	req, _ := http.NewRequest("GET", albumURL, nil)
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cyberdrop: album not found (HTTP %d) for slug %s", resp.StatusCode, slug)
	}

	var album struct {
		Files []struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
			Size int64  `json:"size"`
			Type string `json:"type"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&album); err != nil {
		return nil, err
	}

	if len(album.Files) == 0 {
		return nil, fmt.Errorf("cyberdrop: no files found in album %s", slug)
	}

	var items []model.MediaItem
	for _, f := range album.Files {
		// Resolve each file's download URL
		fileItems, err := s.parseFile(f.Slug)
		if err != nil {
			continue
		}
		// Use album metadata for title/size if available
		if f.Name != "" {
			fileItems[0].Title = f.Name
		}
		if f.Size > 0 {
			fileItems[0].Size = f.Size
		}
		items = append(items, fileItems[0])
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("cyberdrop: could not resolve any files in album %s", slug)
	}

	return items, nil
}

// extractSlug gets the file or album slug from the URL, ignoring any query
// string (e.g. a tracking parameter like "?ref=x" must not end up as part
// of the slug).
func extractSlug(url string) string {
	if idx := strings.IndexByte(url, '?'); idx >= 0 {
		url = url[:idx]
	}
	url = strings.TrimRight(url, "/")
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

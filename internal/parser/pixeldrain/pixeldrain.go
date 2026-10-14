package pixeldrain

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
	"github.com/rajbahakdanyu/scraper/internal/parser/fetch"
)

const apiBase = "https://pixeldrain.com/api"
const userAgent = netutil.BrowserUserAgent

type Scraper struct {
	client  *http.Client
	baseURL string
	sleeper netutil.Sleeper
}

// NewPixeldrainScraperWithOptions creates a Pixeldrain scraper whose
// requests go through proxyURL (when set) and carry cookies.
func NewPixeldrainScraperWithOptions(proxyURL string, cookies []*http.Cookie) (*Scraper, error) {
	client, err := netutil.NewScraperClient(proxyURL, cookies)
	if err != nil {
		return nil, err
	}
	return &Scraper{
		client:  client,
		baseURL: apiBase,
		sleeper: netutil.RealSleeper{},
	}, nil
}

// SetSleeper replaces the retry delay implementation. Call this before
// parsing, not concurrently with it.
func (s *Scraper) SetSleeper(sleeper netutil.Sleeper) { s.sleeper = sleeper }

func (s *Scraper) Name() string {
	return "pixeldrain"
}

func (s *Scraper) Parse(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	if strings.Contains(rawURL, "/l/") {
		return s.parseList(ctx, rawURL)
	}
	if strings.Contains(rawURL, "/u/") {
		return s.parseFile(ctx, rawURL)
	}
	// Direct file or API links: /api/file/ID, /file/ID, /api/file/ID?download
	if strings.Contains(rawURL, "/file/") || strings.Contains(rawURL, "/api/file/") {
		return s.parseFile(ctx, rawURL)
	}
	return nil, fmt.Errorf("pixeldrain: unsupported URL format (expected /l/, /u/, or /file/): %s", rawURL)
}

func (s *Scraper) parseList(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	listID := netutil.PathSlug(rawURL)
	if listID == "" {
		return nil, fmt.Errorf("pixeldrain: could not extract list ID from %s", rawURL)
	}

	apiURL := fmt.Sprintf("%s/list/%s", s.baseURL, listID)

	var result struct {
		Success   bool   `json:"success"`
		Title     string `json:"title"`
		FileCount int    `json:"file_count"`
		Files     []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Size       int64  `json:"size"`
			MimeType   string `json:"mime_type"`
			HashSHA256 string `json:"hash_sha256"`
		} `json:"files"`
	}

	if err := fetch.JSON(ctx, s.client, apiURL, userAgent, s.sleeper, &result); err != nil {
		return nil, fmt.Errorf("pixeldrain list %s: %w", listID, err)
	}

	if !result.Success {
		return nil, fmt.Errorf("pixeldrain list %s: API returned success=false", listID)
	}

	items := make([]model.MediaItem, 0, len(result.Files))
	for _, f := range result.Files {
		items = append(items, model.MediaItem{
			URL:      fmt.Sprintf("%s/file/%s", s.baseURL, f.ID),
			Title:    f.Name,
			Size:     f.Size,
			MimeType: f.MimeType,
			Type:     model.MediaTypeForName(f.Name),
		})
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("pixeldrain: no files in list %s", listID)
	}

	return items, nil
}

func (s *Scraper) parseFile(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	fileID := netutil.PathSlug(rawURL)
	if fileID == "" {
		return nil, fmt.Errorf("pixeldrain: could not extract file ID from %s", rawURL)
	}

	apiURL := fmt.Sprintf("%s/file/%s/info", s.baseURL, fileID)

	var result struct {
		Success    bool   `json:"success"`
		ID         string `json:"id"`
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		MimeType   string `json:"mime_type"`
		HashSHA256 string `json:"hash_sha256"`
	}

	if err := fetch.JSON(ctx, s.client, apiURL, userAgent, s.sleeper, &result); err != nil {
		return nil, fmt.Errorf("pixeldrain file %s: %w", fileID, err)
	}

	if !result.Success {
		return nil, fmt.Errorf("pixeldrain file %s: API returned success=false", fileID)
	}

	return []model.MediaItem{{
		URL:      fmt.Sprintf("%s/file/%s", s.baseURL, fileID),
		Title:    result.Name,
		Size:     result.Size,
		MimeType: result.MimeType,
		Type:     model.MediaTypeForName(result.Name),
	}}, nil
}

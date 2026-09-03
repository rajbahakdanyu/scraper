package turbo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

const apiBase = "https://turbo.cr"
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

type Scraper struct {
	client  *http.Client
	baseURL string
}

func NewTurboScraper() *Scraper {
	client, _ := netutil.NewPublicClient(30 * time.Second)
	return &Scraper{
		client:  client,
		baseURL: apiBase,
	}
}

func NewTurboScraperWithProxy(proxyURL string) (*Scraper, error) {
	client, err := netutil.NewPublicClientWithProxy(30*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return &Scraper{
		client:  client,
		baseURL: apiBase,
	}, nil
}

func (s *Scraper) SetCookies(cookies []*http.Cookie) {
	s.client = netutil.WithCookies(s.client, cookies)
}

func NewTurboScraperWithCookies(cookies []*http.Cookie) *Scraper {
	scraper := NewTurboScraper()
	scraper.SetCookies(cookies)
	return scraper
}

func (s *Scraper) Name() string {
	return "turbo"
}

func (s *Scraper) Parse(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	if strings.Contains(rawURL, "/a/") {
		return s.parseAlbum(ctx, rawURL)
	}
	if strings.Contains(rawURL, "/d/") || strings.Contains(rawURL, "/v/") {
		return s.parseFile(ctx, rawURL)
	}
	return nil, fmt.Errorf("turbo: unsupported URL format (expected /a/, /d/, or /v/): %s", rawURL)
}

func (s *Scraper) parseAlbum(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	body, err := netutil.GetDoc(ctx, s.client, rawURL, userAgent)
	if err != nil {
		return nil, fmt.Errorf("turbo: fetch album page: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, fmt.Errorf("turbo: parse album page: %w", err)
	}

	albumID := extractID(rawURL)
	if albumID == "" {
		return nil, fmt.Errorf("turbo: could not extract album ID from %s", rawURL)
	}

	var items []model.MediaItem
	doc.Find("tr.file-row").Each(func(_ int, sel *goquery.Selection) {
		fileID, _ := sel.Attr("data-id")
		fileName, _ := sel.Attr("data-name")
		sizeStr, _ := sel.Attr("data-size")

		if fileID == "" {
			return
		}

		var size int64
		if sizeStr != "" {
			if n, _ := fmt.Sscanf(sizeStr, "%d", &size); n != 1 {
				size = 0
			}
		}

		items = append(items, model.MediaItem{
			URL:     fmt.Sprintf("%s/d/%s", s.baseURL, fileID),
			Title:   fileName,
			Size:    size,
			Type:    model.MediaTypeForName(fileName),
			Resolve: s.resolveFile(ctx, fileID),
		})
	})

	if len(items) == 0 {
		return nil, fmt.Errorf("turbo: no files found in album %s", albumID)
	}

	return items, nil
}

func (s *Scraper) resolveFile(ctx context.Context, fileID string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		u, err := s.signURL(ctx, fileID)
		return u, model.WrapPermanent(err)
	}
}

func (s *Scraper) parseFile(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	fileID := extractID(rawURL)
	if fileID == "" {
		return nil, fmt.Errorf("turbo: could not extract file ID from %s", rawURL)
	}

	body, err := netutil.GetDoc(ctx, s.client, rawURL, userAgent)
	if err != nil {
		return nil, fmt.Errorf("turbo: fetch file page: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, fmt.Errorf("turbo: parse file page: %w", err)
	}

	fileName := doc.Find("#fileName").Text()
	if fileName == "" {
		return nil, fmt.Errorf("turbo: could not find filename on %s", rawURL)
	}

	var size int64
	sizeText := doc.Find("#fileSizeBytes").Text()
	if sizeText != "" {
		if n, _ := fmt.Sscanf(strings.ReplaceAll(sizeText, ",", ""), "%d", &size); n != 1 {
			size = 0
		}
	}

	return []model.MediaItem{{
		URL:     rawURL,
		Title:   fileName,
		Size:    size,
		Type:    model.MediaTypeForName(fileName),
		Resolve: s.resolveFile(ctx, fileID),
	}}, nil
}

func (s *Scraper) signURL(ctx context.Context, fileID string) (string, error) {
	apiURL := fmt.Sprintf("%s/api/sign?v=%s", s.baseURL, fileID)

	var result struct {
		Success bool   `json:"success"`
		URL     string `json:"url"`
	}

	if err := netutil.GetJSON(ctx, s.client, apiURL, userAgent, &result); err != nil {
		var statusErr *netutil.HTTPStatusError
		if errors.As(err, &statusErr) && !model.IsRetryableStatus(statusErr.StatusCode) {
			return "", model.NewNonRetryableStatus(
				fmt.Errorf("turbo: sign %s: %w", fileID, err), statusErr.StatusCode)
		}
		return "", fmt.Errorf("turbo: sign %s: %w", fileID, err)
	}

	if !result.Success || result.URL == "" {
		return "", model.NewNonRetryable(fmt.Errorf("turbo: sign returned no URL for %s", fileID))
	}

	return result.URL, nil
}

func extractID(rawURL string) string {
	if idx := strings.IndexByte(rawURL, '#'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	if idx := strings.IndexByte(rawURL, '?'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	return parts[len(parts)-1]
}

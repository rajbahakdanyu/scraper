package turbo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
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
	sleeper netutil.Sleeper
}

// SetSleeper replaces the retry delay implementation. Call this before
// parsing, not concurrently with it.
func (s *Scraper) SetSleeper(sleeper netutil.Sleeper) { s.sleeper = sleeper }

func NewTurboScraper() *Scraper {
	client, _ := netutil.NewPublicClient(30 * time.Second)
	return &Scraper{
		client:  client,
		baseURL: apiBase,
		sleeper: netutil.RealSleeper{},
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
		sleeper: netutil.RealSleeper{},
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

// fetchDocWithRetry fetches a page, retrying transient failures (5xx,
// network errors) with backoff; non-retryable HTTP statuses (404, ...) stop
// after one attempt.
func fetchDocWithRetry(ctx context.Context, s *Scraper, rawURL string) (io.Reader, error) {
	var body io.Reader
	err := netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, s.sleeper, func() error {
		var fetchErr error
		body, fetchErr = netutil.GetDoc(ctx, s.client, rawURL, userAgent)
		if fetchErr != nil {
			var statusErr *netutil.HTTPStatusError
			if errors.As(fetchErr, &statusErr) && !model.IsRetryableStatus(statusErr.StatusCode) {
				return model.NewNonRetryable(fetchErr)
			}
			return fetchErr
		}
		return nil
	})
	return body, err
}

func (s *Scraper) parseAlbum(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	body, err := fetchDocWithRetry(ctx, s, rawURL)
	if err != nil {
		return nil, fmt.Errorf("turbo: fetch album page: %w", err)
	}
	html, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("turbo: read album page: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("turbo: parse album page: %w", err)
	}

	albumID := extractID(rawURL)
	if albumID == "" {
		return nil, fmt.Errorf("turbo: could not extract album ID from %s", rawURL)
	}

	// Older pages render the file table server-side; current pages embed
	// the list in a script and build the rows client-side.
	items := extractRowItems(doc, ctx, s)
	if len(items) == 0 {
		items = extractEmbeddedItems(html, ctx, s)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("turbo: no files found in album %s", albumID)
	}

	return items, nil
}

// extractRowItems reads the legacy server-rendered <tr class="file-row">
// table rows.
func extractRowItems(doc *goquery.Document, ctx context.Context, s *Scraper) []model.MediaItem {
	var items []model.MediaItem
	doc.Find("tr.file-row").Each(func(_ int, sel *goquery.Selection) {
		fileID, _ := sel.Attr("data-id")
		if fileID == "" {
			return
		}
		fileName, _ := sel.Attr("data-name")
		sizeStr, _ := sel.Attr("data-size")
		items = append(items, albumItem(ctx, s, fileID, fileName, parseSize(sizeStr)))
	})
	return items
}

// turboEmbeddedFile matches one entry of the album page's embedded
// `const FILES = [{ id:"...", name:"...", size: N, ... }, ...]` array,
// which the page uses to render its file rows client-side.
var turboEmbeddedFile = regexp.MustCompile(`\{\s*id:"([^"]+)"\s*,\s*name:"((?:[^"\\]|\\.)*)"\s*,\s*size:\s*(\d+)`)

// extractEmbeddedItems reads the file list the page embeds for its
// client-side rendering.
func extractEmbeddedItems(html []byte, ctx context.Context, s *Scraper) []model.MediaItem {
	matches := turboEmbeddedFile.FindAllSubmatch(html, -1)
	items := make([]model.MediaItem, 0, len(matches))
	for _, m := range matches {
		fileID := string(m[1])
		name := decodeJSString(string(m[2]))
		items = append(items, albumItem(ctx, s, fileID, name, parseSize(string(m[3]))))
	}
	return items
}

func albumItem(ctx context.Context, s *Scraper, fileID, name string, size int64) model.MediaItem {
	return model.MediaItem{
		URL:     fmt.Sprintf("%s/d/%s", s.baseURL, fileID),
		Title:   name,
		Size:    size,
		Type:    model.MediaTypeForName(name),
		Resolve: s.resolveFile(ctx, fileID),
	}
}

// parseSize parses a byte count, returning 0 for empty or unparsable input.
func parseSize(sizeStr string) int64 {
	if sizeStr == "" {
		return 0
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeStr), 10, 64)
	if err != nil {
		return 0
	}
	return size
}

// decodeJSString unescapes the JavaScript string escapes the payload uses
// (\/, \uXXXX, \", ...); invalid escapes fall back to the raw text.
func decodeJSString(raw string) string {
	if decoded, err := strconv.Unquote(`"` + strings.ReplaceAll(raw, `\/`, `/`) + `"`); err == nil {
		return decoded
	}
	return raw
}

func (s *Scraper) resolveFile(ctx context.Context, fileID string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		return s.signURL(ctx, fileID)
	}
}

func (s *Scraper) parseFile(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	fileID := extractID(rawURL)
	if fileID == "" {
		return nil, fmt.Errorf("turbo: could not extract file ID from %s", rawURL)
	}

	body, err := fetchDocWithRetry(ctx, s, rawURL)
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
	if err := netutil.NewPolicy().ValidateURL(result.URL); err != nil {
		return "", model.NewNonRetryable(fmt.Errorf("turbo: unsafe sign URL for %s: %w", fileID, err))
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

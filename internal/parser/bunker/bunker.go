// internal/parser/bunker/bunker.go
package bunker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
	signURL   string
	sleeper   netutil.Sleeper
}

const (
	// maxAlbumAttempts bounds album-page fetches: transient blips,
	// rate-limit interstitials, and 5xx responses shouldn't fail an
	// otherwise valid album.
	maxAlbumAttempts    = 4
	albumRetryBaseDelay = 1500 * time.Millisecond
)

// NewBunkerScraper creates a new Bunker scraper using the default HTTP client.
func NewBunkerScraper() *Scraper {
	client, _ := netutil.NewPublicClient(30 * time.Second)
	return &Scraper{
		client:    client,
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   "https://bunkr.cr",
		signURL:   "https://glb-apisign.cdn.cr/sign",
		sleeper:   netutil.RealSleeper{},
	}
}

// NewBunkerScraperWithProxy creates a Bunker scraper that routes all
// HTTP requests through the given proxy URL.
func NewBunkerScraperWithProxy(proxyURL string) (*Scraper, error) {
	client, err := netutil.NewPublicClientWithProxy(30*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return &Scraper{
		client:    client,
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   "https://bunkr.cr",
		signURL:   "https://glb-apisign.cdn.cr/sign",
		sleeper:   netutil.RealSleeper{},
	}, nil
}

// SetSleeper replaces the retry delay implementation. Call this before
// parsing, not concurrently with it.
func (s *Scraper) SetSleeper(sleeper netutil.Sleeper) { s.sleeper = sleeper }

func (s *Scraper) SetCookies(cookies []*http.Cookie) {
	s.client = netutil.WithCookies(s.client, cookies)
}

func NewBunkerScraperWithCookies(cookies []*http.Cookie) *Scraper {
	s := NewBunkerScraper()
	s.SetCookies(cookies)
	return s
}

func (s *Scraper) Name() string {
	return "bunker"
}

// Parse fetches a Bunker album page, extracts file links, and visits each
// file page to get the dl.bunkr.cr link. The final signed CDN download URL
// (API + signing service) is NOT resolved at parse time — signatures carry
// an expiry, so each item's Resolve func signs lazily at download time.
func (s *Scraper) Parse(ctx context.Context, albumURL string) ([]model.MediaItem, error) {
	// Strip query params — ?advanced=1 switches to JS-rendered mode
	// which doesn't have server-side file links in the HTML.
	if idx := strings.Index(albumURL, "?"); idx >= 0 {
		albumURL = albumURL[:idx]
	}

	// Single file URLs (/f/ID or /file/ID) — parse directly
	// instead of going through the album page scraper.
	if strings.Contains(albumURL, "/f/") || strings.Contains(albumURL, "/file/") {
		item, err := s.parseFilePage(ctx, albumURL)
		if err != nil {
			return nil, err
		}
		return []model.MediaItem{item}, nil
	}

	fileLinks, err := s.fetchAlbumLinks(ctx, albumURL)
	if err != nil {
		return nil, err
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
			item, err := s.parseFilePage(ctx, u)
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

	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("bunker: could not extract any download URLs: %w", model.NewPartialError("bunker album", errs))
	}

	return result, model.NewPartialError("bunker album", errs)
}

// fetchAlbumLinks fetches an album page and extracts its download links,
// retrying transient failures with backoff: 429/5xx and network errors, and
// a 200 page with no links (a challenge interstitial or momentary blip).
// Permanent errors (404 etc.) stop after one attempt via netutil.Retry's
// NonRetryableError handling.
func (s *Scraper) fetchAlbumLinks(ctx context.Context, albumURL string) ([]string, error) {
	var links []string
	err := netutil.Retry(ctx, maxAlbumAttempts, albumRetryBaseDelay, s.sleeper, func() error {
		doc, err := s.fetchDoc(ctx, albumURL)
		if err != nil {
			return err
		}
		links = nil
		doc.Find(`a[aria-label="download"]`).Each(func(_ int, sel *goquery.Selection) {
			href, ok := sel.Attr("href")
			if !ok || href == "" {
				return
			}
			links = append(links, s.resolveURL(albumURL, href))
		})
		if len(links) == 0 {
			if isJSAlbumPage(doc) {
				return model.NewNonRetryable(fmt.Errorf("bunker: album %s serves its file list via JavaScript (advanced mode), which this scraper cannot read; open the album in a browser to download it", albumURL))
			}
			return fmt.Errorf("bunker: album page %s returned no file links", albumURL)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return links, nil
}

// isJSAlbumPage reports whether an album page is the client-rendered
// "advanced mode" variant, whose file list is built by JavaScript from data
// that is not in the HTML at all (the page only reads window.albumFiles).
func isJSAlbumPage(doc *goquery.Document) bool {
	text := doc.Text()
	return strings.Contains(text, "albumFiles") || strings.Contains(text, "isAdvancedMode")
}

// parseFilePage visits a /f/<id> page to get the dl.bunkr.cr download link
// and the file's title — the stable metadata. The signed CDN URL itself is
// deferred to the item's Resolve func: the API + signing exchange runs at
// download time so the signature can't expire sitting in the queue.
func (s *Scraper) parseFilePage(ctx context.Context, fileURL string) (model.MediaItem, error) {
	doc, err := s.fetchDoc(ctx, fileURL)
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

	// Extract the dl.bunkr.cr origin for the API call. The file ID comes
	// from the parsed URL path afterwards so a query string on the href
	// (?download=1) can't leak into the API's file ID.
	dlURL, err := url.Parse(dlLink)
	if err != nil || dlURL == nil {
		return model.MediaItem{}, fmt.Errorf("invalid dl.bunkr URL: %s", dlLink)
	}
	if !dlURL.IsAbs() {
		base, parseErr := url.Parse(fileURL)
		if parseErr != nil {
			return model.MediaItem{}, parseErr
		}
		dlURL = base.ResolveReference(dlURL)
	}
	// e.g. https://dl.bunkr.cr/file/61567794
	fileID := path.Base(dlURL.Path)
	if fileID == "" || fileID == "/" || fileID == "." {
		return model.MediaItem{}, fmt.Errorf("could not extract file ID from %s", dlLink)
	}
	if err := netutil.NewPolicy(s.baseURL).ValidateURL(dlURL.String()); err != nil {
		return model.MediaItem{}, fmt.Errorf("bunker: unsafe download API URL: %w", err)
	}

	if title == "" {
		title = fileID
	}

	// The item carries the stable dl.bunkr.cr URL (meaningful for display,
	// unique per file for MediaItem.Key()); the signed CDN URL is produced
	// lazily at download time via the API + signing service.
	return model.MediaItem{
		URL:      dlURL.String(),
		PageURL:  fileURL,
		Title:    title,
		MimeType: "application/octet-stream",
		Type:     model.MediaTypeForName(title),
		Resolve: func(ctx context.Context) (string, error) {
			signedURL, err := s.getSignedMediaURL(ctx, dlURL, fileID, title)
			return signedURL, model.WrapPermanent(err)
		},
	}, nil
}

// getSignedMediaURL replicates the bunkr download flow:
// 1. POST to /api/_001_v2 with the file ID → get CDN base + path
// 2. Sign the URL via glb-apisign.cdn.cr/sign
func (s *Scraper) getSignedMediaURL(ctx context.Context, dlURL *url.URL, fileID, originalName string) (string, error) {
	apiURL := dlURL.Scheme + "://" + dlURL.Host + "/api/_001_v2"

	// Step 1: POST to get media metadata
	body, err := json.Marshal(map[string]string{"id": fileID})
	if err != nil {
		return "", err
	}
	var meta struct {
		MediaFiles string `json:"mediafiles"`
		Path       string `json:"path"`
	}
	err = netutil.PostJSON(ctx, s.client, apiURL, map[string]string{
		"Content-Type": "application/json",
		"Referer":      dlURL.String(),
		"User-Agent":   s.userAgent,
	}, body, &meta)
	if err != nil {
		var statusErr *netutil.HTTPStatusError
		if errors.As(err, &statusErr) {
			statusErrMessage := fmt.Errorf("bunker API returned HTTP %d", statusErr.StatusCode)
			if !model.IsRetryableStatus(statusErr.StatusCode) {
				return "", model.NewNonRetryable(statusErrMessage)
			}
			return "", statusErrMessage
		}
		return "", err
	}

	if meta.MediaFiles == "" || meta.Path == "" {
		return "", fmt.Errorf("bunker API returned empty media URL")
	}

	rawURL := meta.MediaFiles + meta.Path
	if err := netutil.NewPolicy().ValidateURL(rawURL); err != nil {
		return "", fmt.Errorf("bunker: unsafe media URL: %w", err)
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Step 2: Sign the URL
	signReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.signURL+"?path="+url.QueryEscape(parsedURL.Path), nil)
	if err != nil {
		return "", fmt.Errorf("bunker: build signing request: %w", err)
	}
	signReq.Header.Set("User-Agent", s.userAgent)
	signResp, err := s.client.Do(signReq)
	if err != nil {
		return "", fmt.Errorf("bunker: signing request: %w", err)
	}
	defer signResp.Body.Close()
	if signResp.StatusCode != http.StatusOK {
		err := fmt.Errorf("bunker signing API returned HTTP %d", signResp.StatusCode)
		if !model.IsRetryableStatus(signResp.StatusCode) {
			return "", model.NewNonRetryable(err)
		}
		return "", err
	}

	var sign struct {
		Token string `json:"token"`
		Ex    int64  `json:"ex"`
	}
	if err := json.NewDecoder(io.LimitReader(signResp.Body, 10<<20)).Decode(&sign); err != nil {
		return "", fmt.Errorf("bunker: decode signing response: %w", err)
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

func (s *Scraper) fetchDoc(ctx context.Context, pageURL string) (*goquery.Document, error) {
	reader, err := netutil.GetDoc(ctx, s.client, pageURL, s.userAgent)
	if err != nil {
		var statusErr *netutil.HTTPStatusError
		if errors.As(err, &statusErr) {
			statusMessage := fmt.Errorf("bunker: HTTP %d for %s", statusErr.StatusCode, pageURL)
			if !model.IsRetryableStatus(statusErr.StatusCode) {
				return nil, model.NewNonRetryable(statusMessage)
			}
			return nil, statusMessage
		}
		return nil, err
	}
	return goquery.NewDocumentFromReader(reader)
}

// resolveURL resolves an href against the page URL it was found on.
// ResolveReference handles absolute URLs, protocol-relative hrefs
// (//host/path, inheriting the page scheme), absolute paths and relative
// paths per RFC 3986.
func (s *Scraper) resolveURL(pageURL, href string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(ref).String()
}

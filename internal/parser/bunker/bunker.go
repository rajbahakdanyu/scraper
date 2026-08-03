// internal/parser/bunker/bunker.go
package bunker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

// NewBunkerScraper creates a new Bunker scraper using the default HTTP client.
func NewBunkerScraper() *Scraper {
	return &Scraper{
		client:    netutil.NewPublicClient(30 * time.Second),
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   "https://bunkr.cr",
		signURL:   "https://glb-apisign.cdn.cr/sign",
	}
}

func (s *Scraper) Name() string {
	return "bunker"
}

// nonRetryableError marks an error that will never succeed no matter how
// many times it's retried (e.g. an HTTP 4xx client error other than
// 408/429). Mirrors the downloader's error classification; deliberately
// duplicated rather than shared (see BACKLOG.md).
type nonRetryableError struct{ err error }

func (e *nonRetryableError) Error() string { return e.err.Error() }
func (e *nonRetryableError) Unwrap() error { return e.err }

// isRetryableStatus reports whether an HTTP status code represents a
// transient failure worth retrying (5xx, request timeout, rate limiting)
// as opposed to a client error (bad URL, forbidden, not found, ...) that
// will never succeed no matter how many times it's retried.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	default:
		return code >= 500
	}
}

// permanentIfNotRetryable translates a permanent resolve-API failure (a
// 4xx other than 408/429 — e.g. the file is gone upstream) into a
// model.PermanentError so the downloader fails the item immediately
// instead of retrying with backoff. Transient failures (5xx, 408, 429,
// network errors) pass through unwrapped and keep the downloader's retry
// behavior.
func permanentIfNotRetryable(signedURL string, err error) (string, error) {
	if err == nil {
		return signedURL, nil
	}
	var nre *nonRetryableError
	if errors.As(err, &nre) {
		return "", &model.PermanentError{Err: err}
	}
	return "", err
}

// Parse fetches a Bunker album page, extracts file links, and visits each
// file page to get the dl.bunkr.cr link. The final signed CDN download URL
// (API + signing service) is NOT resolved at parse time — signatures carry
// an expiry, so each item's Resolve func signs lazily at download time.
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
		fullURL := s.resolveURL(albumURL, href)
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

// parseFilePage visits a /f/<id> page to get the dl.bunkr.cr download link
// and the file's title — the stable metadata. The signed CDN URL itself is
// deferred to the item's Resolve func: the API + signing exchange runs at
// download time so the signature can't expire sitting in the queue.
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
		Title:    title,
		MimeType: "application/octet-stream",
		Type:     model.MediaTypeForName(title),
		Resolve: func(ctx context.Context) (string, error) {
			return permanentIfNotRetryable(s.getSignedMediaURL(ctx, dlURL, fileID, title))
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
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
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
		err := fmt.Errorf("bunker API returned HTTP %d", resp.StatusCode)
		if !isRetryableStatus(resp.StatusCode) {
			return "", &nonRetryableError{err}
		}
		return "", err
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
	if err := netutil.NewPolicy().ValidateURL(rawURL); err != nil {
		return "", fmt.Errorf("bunker: unsafe media URL: %w", err)
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Step 2: Sign the URL
	signReq, err := http.NewRequestWithContext(ctx, "GET", s.signURL+"?path="+url.QueryEscape(parsedURL.Path), nil)
	if err != nil {
		return "", fmt.Errorf("bunker: build signing request: %w", err)
	}
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

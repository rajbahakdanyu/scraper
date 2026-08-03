// internal/parser/cyberdrop/cyberdrop.go
package cyberdrop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
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
		client:    netutil.NewPublicClient(30 * time.Second),
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   apiBase,
	}
}

func (s *Scraper) Name() string {
	return "cyberdrop"
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

// Parse handles both file URLs (/f/<slug>) and album URLs (/a/<slug>).
// The signed CDN download URL is never resolved at parse time — signatures
// expire, so an item queued behind a long album download would hold a
// stale signature. Instead each item carries a Resolve func that performs
// the signed-URL exchange lazily at download time (and again on a 403).
func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
	slug := extractSlug(url)
	if slug == "" {
		return nil, fmt.Errorf("cyberdrop: could not extract slug from URL: %s", url)
	}

	if strings.Contains(url, "/a/") || strings.Contains(url, "/album/") {
		return s.parseAlbum(url, slug)
	}
	item, err := s.parseFile(context.Background(), url, slug)
	if err != nil {
		return nil, err
	}
	return []model.MediaItem{item}, nil
}

// fileInfo is the subset of the /file/info response the scraper uses.
type fileInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	AuthURL string `json:"auth_url"`
}

// fetchFileInfo GETs a file's metadata (name, size, auth_url) from the API.
func (s *Scraper) fetchFileInfo(ctx context.Context, slug string) (fileInfo, error) {
	infoURL := fmt.Sprintf("%s/file/info/%s", s.baseURL, slug)

	infoReq, err := http.NewRequestWithContext(ctx, "GET", infoURL, nil)
	if err != nil {
		return fileInfo{}, fmt.Errorf("cyberdrop: build file-info request: %w", err)
	}
	infoReq.Header.Set("User-Agent", s.userAgent)

	infoResp, err := s.client.Do(infoReq)
	if err != nil {
		return fileInfo{}, err
	}
	defer infoResp.Body.Close()

	if infoResp.StatusCode != http.StatusOK {
		err := fmt.Errorf("cyberdrop: file not found (HTTP %d) for slug %s", infoResp.StatusCode, slug)
		if !isRetryableStatus(infoResp.StatusCode) {
			return fileInfo{}, &nonRetryableError{err}
		}
		return fileInfo{}, err
	}

	var info fileInfo
	if err := json.NewDecoder(infoResp.Body).Decode(&info); err != nil {
		return fileInfo{}, err
	}

	if info.AuthURL == "" {
		return fileInfo{}, fmt.Errorf("cyberdrop: no auth_url returned for %s", slug)
	}
	if err := netutil.NewPolicy(s.baseURL).ValidateURL(info.AuthURL); err != nil {
		return fileInfo{}, fmt.Errorf("cyberdrop: unsafe auth_url for %s: %w", slug, err)
	}

	return info, nil
}

// fetchSignedURL exchanges a file's auth_url for the signed CDN download URL.
func (s *Scraper) fetchSignedURL(ctx context.Context, authURL, slug string) (string, error) {
	authReq, err := http.NewRequestWithContext(ctx, "GET", authURL, nil)
	if err != nil {
		return "", fmt.Errorf("cyberdrop: invalid auth_url for %s: %w", slug, err)
	}
	authReq.Header.Set("User-Agent", s.userAgent)

	authResp, err := s.client.Do(authReq)
	if err != nil {
		return "", err
	}
	defer authResp.Body.Close()

	if authResp.StatusCode != http.StatusOK {
		err := fmt.Errorf("cyberdrop: failed to get download URL (HTTP %d)", authResp.StatusCode)
		if !isRetryableStatus(authResp.StatusCode) {
			return "", &nonRetryableError{err}
		}
		return "", err
	}

	var auth struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(authResp.Body).Decode(&auth); err != nil {
		return "", err
	}

	if auth.URL == "" {
		return "", fmt.Errorf("cyberdrop: empty download URL for %s", slug)
	}
	if err := netutil.NewPolicy().ValidateURL(auth.URL); err != nil {
		return "", fmt.Errorf("cyberdrop: unsafe download URL for %s: %w", slug, err)
	}

	return auth.URL, nil
}

// parseFile fetches a single file's metadata eagerly (it carries the real
// filename and size, and surfaces missing/unsafe files at parse time) and
// defers only the signed-URL exchange to the item's Resolve func.
func (s *Scraper) parseFile(ctx context.Context, pageURL, slug string) (model.MediaItem, error) {
	info, err := s.fetchFileInfo(ctx, slug)
	if err != nil {
		return model.MediaItem{}, err
	}

	title := info.Name
	if title == "" {
		title = slug
	}

	return model.MediaItem{
		URL:      pageURL,
		Title:    title,
		Size:     info.Size,
		MimeType: info.Type,
		Type:     model.MediaTypeForName(title),
		Resolve: func(ctx context.Context) (string, error) {
			return permanentIfNotRetryable(s.fetchSignedURL(ctx, info.AuthURL, slug))
		},
	}, nil
}

// parseAlbum fetches the album listing and builds one item per file
// straight from the listing metadata — no per-file requests at all. Each
// item's Resolve func performs that file's full info+auth exchange at
// download time, so a resolution failure surfaces per item then, not as a
// parse-time PartialError.
func (s *Scraper) parseAlbum(albumPageURL, slug string) ([]model.MediaItem, error) {
	albumURL := fmt.Sprintf("%s/album/%s", s.baseURL, slug)

	req, err := http.NewRequest("GET", albumURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cyberdrop: build album request: %w", err)
	}
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

	// The placeholder URL keeps the file's stable page URL — meaningful for
	// display and unique per file for MediaItem.Key().
	pageOrigin := ""
	if u, err := url.Parse(albumPageURL); err == nil {
		pageOrigin = u.Scheme + "://" + u.Host
	}

	items := make([]model.MediaItem, 0, len(album.Files))
	for _, f := range album.Files {
		fileSlug := f.Slug
		title := f.Name
		if title == "" {
			title = fileSlug
		}
		items = append(items, model.MediaItem{
			URL:      pageOrigin + "/f/" + fileSlug,
			Title:    title,
			Size:     f.Size,
			MimeType: f.Type,
			Type:     model.MediaTypeForName(title),
			Resolve: func(ctx context.Context) (string, error) {
				info, err := s.fetchFileInfo(ctx, fileSlug)
				if err != nil {
					return permanentIfNotRetryable("", err)
				}
				return permanentIfNotRetryable(s.fetchSignedURL(ctx, info.AuthURL, fileSlug))
			},
		})
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

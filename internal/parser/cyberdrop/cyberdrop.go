// internal/parser/cyberdrop/cyberdrop.go
package cyberdrop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

const apiBase = "https://api.cyberdrop.cr/api"

type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
	sleeper   netutil.Sleeper
}

// NewCyberDropScraperWithOptions creates a CyberDrop scraper whose requests
// go through proxyURL (when set) and carry cookies.
func NewCyberDropScraperWithOptions(proxyURL string, cookies []*http.Cookie) (*Scraper, error) {
	client, err := netutil.NewScraperClient(proxyURL, cookies)
	if err != nil {
		return nil, err
	}
	return &Scraper{
		client:    client,
		userAgent: netutil.BrowserUserAgent,
		baseURL:   apiBase,
		sleeper:   netutil.RealSleeper{},
	}, nil
}

// SetSleeper replaces the retry delay implementation. Call this before
// parsing, not concurrently with it.
func (s *Scraper) SetSleeper(sleeper netutil.Sleeper) { s.sleeper = sleeper }

// fetchJSONWithRetry fetches and decodes JSON, retrying transient failures
// (5xx, network errors) with backoff; non-retryable HTTP statuses (404, ...)
// stop after one attempt.
func fetchJSONWithRetry(ctx context.Context, s *Scraper, url string, target interface{}) error {
	return netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, s.sleeper, func() error {
		err := netutil.GetJSON(ctx, s.client, url, s.userAgent, target)
		if err != nil {
			var statusErr *netutil.HTTPStatusError
			if errors.As(err, &statusErr) && !model.IsRetryableStatus(statusErr.StatusCode) {
				return model.NewNonRetryable(err)
			}
			return err
		}
		return nil
	})
}

func (s *Scraper) Name() string {
	return "cyberdrop"
}

func classifyAPIError(err error, message string, args ...interface{}) error {
	var statusErr *netutil.HTTPStatusError
	if !errors.As(err, &statusErr) {
		return err
	}
	statusArgs := append([]interface{}{statusErr.StatusCode}, args...)
	statusError := fmt.Errorf(message, statusArgs...)
	if !model.IsRetryableStatus(statusErr.StatusCode) {
		return model.NewNonRetryable(statusError)
	}
	return statusError
}

// Parse handles both file URLs (/f/<slug>) and album URLs (/a/<slug>).
// The signed CDN download URL is never resolved at parse time — signatures
// expire, so an item queued behind a long album download would hold a
// stale signature. Instead each item carries a Resolve func that performs
// the signed-URL exchange lazily at download time (and again on a 403).
func (s *Scraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	slug := extractSlug(url)
	if slug == "" {
		return nil, fmt.Errorf("cyberdrop: could not extract slug from URL: %s", url)
	}

	if strings.Contains(url, "/a/") || strings.Contains(url, "/album/") {
		return s.parseAlbum(ctx, url, slug)
	}
	item, err := s.parseFile(ctx, url, slug)
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
func (s *Scraper) fetchFileInfo(ctx context.Context, slug, pageURL string) (fileInfo, error) {
	infoURL := fmt.Sprintf("%s/file/info/%s", s.baseURL, slug)

	var info fileInfo
	if err := netutil.GetJSON(ctx, s.client, infoURL, s.userAgent, &info); err != nil {
		return fileInfo{}, classifyAPIError(err, "cyberdrop: file not found (HTTP %d) for slug %s", slug)
	}

	if info.AuthURL == "" {
		return fileInfo{}, fmt.Errorf("cyberdrop: no auth_url returned for %s", slug)
	}
	if err := netutil.NewStrictPolicy(s.baseURL, pageURL).ValidateURL(info.AuthURL); err != nil {
		return fileInfo{}, fmt.Errorf("cyberdrop: unsafe auth_url for %s: %w", slug, err)
	}

	return info, nil
}

// fetchSignedURL exchanges a file's auth_url for the signed CDN download URL.
func (s *Scraper) fetchSignedURL(ctx context.Context, authURL, slug string) (string, error) {
	var auth struct {
		URL string `json:"url"`
	}
	// authURL already passed strict host validation in fetchFileInfo; pin
	// its redirects to the same hosts so an allowed auth host cannot bounce
	// the request to an arbitrary public host.
	authClient := netutil.WithRedirectPolicy(s.client, netutil.NewStrictPolicy(s.baseURL, authURL))
	if err := netutil.GetJSON(ctx, authClient, authURL, s.userAgent, &auth); err != nil {
		return "", classifyAPIError(err, "cyberdrop: failed to get download URL (HTTP %d)")
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
	info, err := s.fetchFileInfo(ctx, slug, pageURL)
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
			return s.fetchSignedURL(ctx, info.AuthURL, slug)
		},
	}, nil
}

// parseAlbum fetches the album listing and builds one item per file
// straight from the listing metadata — no per-file requests at all. Each
// item's Resolve func performs that file's full info+auth exchange at
// download time, so a resolution failure surfaces per item then, not as a
// parse-time PartialError.
func (s *Scraper) parseAlbum(ctx context.Context, albumPageURL, slug string) ([]model.MediaItem, error) {
	albumURL := fmt.Sprintf("%s/album/%s", s.baseURL, slug)

	var album struct {
		Files []struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
			Size int64  `json:"size"`
			Type string `json:"type"`
		} `json:"files"`
	}
	if err := fetchJSONWithRetry(ctx, s, albumURL, &album); err != nil {
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
				info, err := s.fetchFileInfo(ctx, fileSlug, albumPageURL)
				if err != nil {
					return "", err
				}
				return s.fetchSignedURL(ctx, info.AuthURL, fileSlug)
			},
		})
	}

	return items, nil
}

// extractSlug gets the file or album slug from the URL, ignoring any query
// string (e.g. a tracking parameter like "?ref=x" must not end up as part
// of the slug).
func extractSlug(url string) string {
	return netutil.PathSlug(url)
}

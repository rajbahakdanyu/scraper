// internal/parser/filester/filester.go
package filester

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
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
		client:    netutil.NewPublicClient(30 * time.Second),
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
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
		// A single file stays eager, unlike folder items: the token
		// exchange is the only source of the file's real name (a lazy
		// item's Title must carry the real extension for the downloader's
		// filename), and a queue of one starts downloading immediately so
		// the signature can't expire in the queue.
		item, err := s.resolveFile(context.Background(), id, "")
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

const (
	// maxPageRetries is how many times a single folder-listing page is
	// retried if it comes back empty/unreachable before giving up on it.
	maxPageRetries = 4
	// pageRetryBaseDelay is the base exponential-backoff delay between
	// retries of the same page (doubles each attempt: 1.5s, 3s, 6s).
	pageRetryBaseDelay = 1500 * time.Millisecond
	// pageFetchDelay is a small politeness delay between different pages,
	// to reduce the odds of a large (dozens-of-pages) album tripping
	// rate limiting from rapid back-to-back requests in the first place.
	pageFetchDelay = 400 * time.Millisecond

	// maxResolveRetries/resolveRetryBaseDelay mirror the page-retry
	// settings above but for resolveFile's download-token requests.
	maxResolveRetries     = 4
	resolveRetryBaseDelay = 1500 * time.Millisecond
)

// backoffWithJitter returns an exponentially increasing delay (base *
// 2^(attempt-1), attempt starting at 1) with up to ~30% random jitter
// added on top, so many concurrent callers retrying around the same time
// (e.g. resolveFile's worker pool) don't all land on the exact same
// instant and re-trigger whatever rate limiting caused the retry.
func backoffWithJitter(base time.Duration, attempt int) time.Duration {
	d := base << uint(attempt-1)
	// #nosec G404 -- retry jitter does not require cryptographic randomness.
	jitter := time.Duration(rand.Int63n(int64(d)/3 + 1))
	return d + jitter
}

// nonRetryableError marks an error that will never succeed no matter how
// many times it's retried (e.g. an HTTP 4xx client error other than
// 408/429, or an undecodable/invalid token response that retrying can't
// fix). Mirrors the downloader's error classification; deliberately
// duplicated rather than shared (see BACKLOG.md).
type nonRetryableError struct{ err error }

func (e *nonRetryableError) Error() string { return e.err.Error() }
func (e *nonRetryableError) Unwrap() error { return e.err }

// isRetryableStatus reports whether an HTTP status code represents a
// transient failure worth retrying (5xx, request timeout, rate limiting) as
// opposed to a client error (bad URL, forbidden, not found, ...) that will
// never succeed no matter how many times it's retried.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	default:
		return code >= 500
	}
}

// parseFolder fetches every page of a folder listing and builds one item
// per file straight from the listing metadata — no per-file requests at
// all. Each item's Resolve func exchanges the file's slug for a signed
// download token lazily at download time (tokens expire, so resolving a
// whole folder eagerly would hand later queue items stale signatures; a
// token-exchange failure surfaces per item then, not as a parse-time
// PartialError).
//
// A folder-listing request can transiently come back with zero items
// (rate limiting, a momentary server blip) even though we already know
// from page 1's "load all pages" total that more pages exist. Treating
// that as "end of album" used to silently truncate large albums (e.g. an
// album with 1023 files across 43 pages stopping after ~15 pages the
// moment one page hiccupped). Once the real page count is known, every
// page is retried a few times and, failing that, skipped rather than
// aborting the rest of the scan.
func (s *Scraper) parseFolder(id string) ([]model.MediaItem, error) {
	var allEntries []folderEntry
	var partialErrs []error
	total := 1

	for page := 1; page <= total; page++ {
		if page > 1 {
			time.Sleep(pageFetchDelay)
		}

		entries, pageTotal, err := s.fetchFolderPage(id, page)

		if page == 1 {
			if err != nil {
				return nil, err
			}
			if pageTotal > total {
				total = pageTotal
			}
		} else if err != nil {
			// Couldn't recover this specific page even after retries.
			// We already know the real page count from page 1's total,
			// so skip it rather than truncating everything after it.
			partialErrs = append(partialErrs, fmt.Errorf("page %d: %w", page, err))
			continue
		}

		allEntries = append(allEntries, entries...)
	}

	if len(allEntries) == 0 {
		return nil, fmt.Errorf("filester: no files found in folder %s", id)
	}

	items := make([]model.MediaItem, 0, len(allEntries))
	for _, e := range allEntries {
		entry := e
		title := entry.name
		if title == "" {
			title = entry.slug
		}
		items = append(items, model.MediaItem{
			// The placeholder URL keeps the file's stable page URL —
			// meaningful for display and unique per file for
			// MediaItem.Key().
			URL:      fmt.Sprintf("%s/d/%s", s.baseURL, entry.slug),
			Title:    title,
			Size:     entry.size,
			MimeType: "application/octet-stream",
			Type:     model.MediaTypeForName(title),
			Resolve: func(ctx context.Context) (string, error) {
				item, err := s.resolveFile(ctx, entry.slug, entry.name)
				if err != nil {
					// Keep the permanent/transient classification
					// across the package boundary: a 404 from the
					// token endpoint can never succeed, so the
					// downloader fails the item immediately instead
					// of retrying with backoff.
					var nre *nonRetryableError
					if errors.As(err, &nre) {
						return "", &model.PermanentError{Err: err}
					}
					return "", err
				}
				return item.URL, nil
			},
		})
	}

	return items, model.NewPartialError("filester folder", partialErrs)
}

// fetchFolderPage fetches one page of a folder listing. A genuine fetch
// error (bad status, network failure) is returned immediately — retrying a
// permanent error like a 404 would just waste time. But if the page loads
// fine yet comes back with zero .file-item entries (and it isn't page 1,
// which can legitimately be an empty folder), that's retried a few times
// before being treated as a real failure: a large album means dozens of
// sequential requests, and a single transient hiccup (rate limiting, a
// momentary blip) shouldn't be mistaken for having reached the end of the
// album — that's exactly what previously caused large albums to be
// silently truncated partway through with no error at all.
func (s *Scraper) fetchFolderPage(id string, page int) (entries []folderEntry, pageTotal int, err error) {
	pageURL := fmt.Sprintf("%s/f/%s?page=%d", s.baseURL, id, page)

	doc, err := s.fetchDoc(pageURL)
	if err != nil {
		return nil, 0, err
	}

	if page == 1 {
		if v, ok := doc.Find("#loadAllPagesBtn").Attr("data-total"); ok {
			if n, aerr := strconv.Atoi(v); aerr == nil {
				pageTotal = n
			}
		}
	}

	entries = extractFileItems(doc)
	if len(entries) > 0 || page == 1 {
		return entries, pageTotal, nil
	}

	for attempt := 1; attempt < maxPageRetries; attempt++ {
		time.Sleep(backoffWithJitter(pageRetryBaseDelay, attempt))

		doc, ferr := s.fetchDoc(pageURL)
		if ferr != nil {
			var nre *nonRetryableError
			if errors.As(ferr, &nre) {
				// A permanent error (e.g. the folder 404ing mid-scan)
				// can never recover — stop retrying this page now
				// instead of sleeping through the remaining attempts.
				return nil, pageTotal, ferr
			}
			continue
		}
		if entries = extractFileItems(doc); len(entries) > 0 {
			return entries, pageTotal, nil
		}
	}

	return nil, pageTotal, fmt.Errorf("filester: page %d of folder %s returned no items after %d attempts", page, id, maxPageRetries)
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
//
// Folder items call this lazily at download time via their Resolve func,
// from concurrent download goroutines across potentially 1000+ files — a
// much burstier pattern than the paced page-listing phase, and one that
// was found to hit the same kind of rate limiting with zero retry
// coverage at all. Retries with backoff+jitter before giving up — except
// for permanent failures (a 4xx other than 408/429, an undecodable or
// invalid token response), which no amount of retrying can fix and so are
// returned immediately.
func (s *Scraper) resolveFile(ctx context.Context, slug, knownName string) (model.MediaItem, error) {
	var lastErr error
	for attempt := 1; attempt <= maxResolveRetries; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return model.MediaItem{}, ctx.Err()
			case <-time.After(backoffWithJitter(resolveRetryBaseDelay, attempt-1)):
			}
		}
		item, err := s.resolveFileOnce(ctx, slug, knownName)
		if err == nil {
			return item, nil
		}
		var nre *nonRetryableError
		if errors.As(err, &nre) {
			// Client errors (404, 403, ...) and token responses that
			// fail to decode or validate will never succeed no matter
			// how many times we retry — stop immediately.
			return model.MediaItem{}, err
		}
		lastErr = err
	}
	return model.MediaItem{}, lastErr
}

// resolveFileOnce is a single attempt at the token exchange described on
// resolveFile.
func (s *Scraper) resolveFileOnce(ctx context.Context, slug, knownName string) (model.MediaItem, error) {
	body, err := json.Marshal(map[string]string{"file_slug": slug})
	if err != nil {
		return model.MediaItem{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/v2/api/public/download", bytes.NewReader(body))
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
		err := fmt.Errorf("filester: download token request failed (HTTP %d) for %s", resp.StatusCode, slug)
		if !isRetryableStatus(resp.StatusCode) {
			return model.MediaItem{}, &nonRetryableError{err}
		}
		return model.MediaItem{}, err
	}

	var tok struct {
		File   string `json:"file"`
		Token  string `json:"token"`
		Server string `json:"server"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return model.MediaItem{}, &nonRetryableError{err}
	}
	if tok.File == "" || tok.Token == "" {
		return model.MediaItem{}, &nonRetryableError{fmt.Errorf("filester: incomplete download token for %s", slug)}
	}

	server := tok.Server
	if server == "" {
		if len(s.cdnURLs) == 0 {
			return model.MediaItem{}, &nonRetryableError{fmt.Errorf("filester: no CDN server available for %s", slug)}
		}
		// #nosec G404 -- CDN load distribution is not a security decision.
		server = s.cdnURLs[rand.Intn(len(s.cdnURLs))]
	}
	allowedURLs := append([]string{s.baseURL}, s.cdnURLs...)
	if err := netutil.NewPolicy(allowedURLs...).ValidateURL(server); err != nil {
		return model.MediaItem{}, &nonRetryableError{fmt.Errorf("filester: unsafe CDN server for %s: %w", slug, err)}
	}
	for _, segment := range strings.Split(tok.File, "/") {
		if segment == "." || segment == ".." {
			return model.MediaItem{}, &nonRetryableError{fmt.Errorf("filester: unsafe CDN path for %s", slug)}
		}
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

	downloadURL, err := url.JoinPath(server, "v2", tok.File)
	if err != nil {
		return model.MediaItem{}, err
	}
	parsedDownloadURL, err := url.Parse(downloadURL)
	if err != nil {
		return model.MediaItem{}, err
	}
	query := parsedDownloadURL.Query()
	query.Set("token", tok.Token)
	query.Set("download", "true")
	query.Set("n", title)
	parsedDownloadURL.RawQuery = query.Encode()
	downloadURL = parsedDownloadURL.String()

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
		err := fmt.Errorf("filester: HTTP %d for %s", resp.StatusCode, pageURL)
		if !isRetryableStatus(resp.StatusCode) {
			return nil, &nonRetryableError{err}
		}
		return nil, err
	}

	return goquery.NewDocumentFromReader(resp.Body)
}

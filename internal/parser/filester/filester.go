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
	"time"

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
		client:    &http.Client{Timeout: 30 * time.Second},
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
	// resolveConcurrency bounds how many download-token requests run at
	// once. This used to be 10 with zero retry coverage — a much burstier
	// request pattern than the (now paced) page-listing phase, and a
	// prime suspect for additional silent losses on large albums. Lowered
	// alongside adding retries so the two changes compound instead of
	// fighting each other.
	resolveConcurrency = 4
)

// backoffWithJitter returns an exponentially increasing delay (base *
// 2^(attempt-1), attempt starting at 1) with up to ~30% random jitter
// added on top, so many concurrent callers retrying around the same time
// (e.g. resolveFile's worker pool) don't all land on the exact same
// instant and re-trigger whatever rate limiting caused the retry.
func backoffWithJitter(base time.Duration, attempt int) time.Duration {
	d := base << uint(attempt-1)
	jitter := time.Duration(rand.Int63n(int64(d)/3 + 1))
	return d + jitter
}

// parseFolder fetches every page of a folder listing, then resolves each
// file's real download URL concurrently.
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
			continue
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
	sem := make(chan struct{}, resolveConcurrency)

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
// This runs with resolveConcurrency requests in flight at once across
// potentially 1000+ files — a much burstier pattern than the paced
// page-listing phase, and one that was found to hit the same kind of
// rate limiting with zero retry coverage at all: any single failed
// token request was silently dropped from the final result with no
// error surfaced. Retries with backoff+jitter before giving up.
func (s *Scraper) resolveFile(slug, knownName string) (model.MediaItem, error) {
	var lastErr error
	for attempt := 1; attempt <= maxResolveRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(backoffWithJitter(resolveRetryBaseDelay, attempt-1))
		}
		item, err := s.resolveFileOnce(slug, knownName)
		if err == nil {
			return item, nil
		}
		lastErr = err
	}
	return model.MediaItem{}, lastErr
}

// resolveFileOnce is a single attempt at the token exchange described on
// resolveFile.
func (s *Scraper) resolveFileOnce(slug, knownName string) (model.MediaItem, error) {
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

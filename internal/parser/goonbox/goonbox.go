// internal/parser/goonbox/goonbox.go
package goonbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

const apiBase = "https://goonbox.cr/api"

// siteOrigin is sent as the Referer on every item download: the image CDN
// rejects requests without it (HTTP 403 "not allowed").
const siteOrigin = "https://goonbox.cr/"

// maxAlbumPages bounds pagination: far above any real album, so only a buggy or hostile API ever hits it.
const maxAlbumPages = 1000

type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
	sleeper   netutil.Sleeper
}

// albumPage is one page of the album API response.
type albumPage struct {
	Images []struct {
		OriginalURL      string `json:"original_url"`
		OriginalFilename string `json:"original_filename"`
		SizeBytes        int64  `json:"size_bytes"`
		Mime             string `json:"mime"`
	} `json:"images"`
	Pagination struct {
		CurrentPage int `json:"current_page"`
		LastPage    int `json:"last_page"`
	} `json:"pagination"`
}

type pageResult struct {
	index      int
	items      []model.MediaItem
	unsafeErrs []error
	current    int
	last       int
	err        error
	fetched    bool
}

// NewGoonBoxScraperWithOptions creates a GoonBox scraper whose requests go
// through proxyURL (when set) and carry cookies.
func NewGoonBoxScraperWithOptions(proxyURL string, cookies []*http.Cookie) (*Scraper, error) {
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

func (s *Scraper) Name() string {
	return "goonbox"
}

func (s *Scraper) Parse(ctx context.Context, albumURL string) ([]model.MediaItem, error) {
	albumID := extractAlbumID(albumURL)
	if albumID == "" {
		return nil, fmt.Errorf("goonbox: could not extract album ID from URL: %s", albumURL)
	}

	fetchPage := func(page int) pageResult {
		url := fmt.Sprintf("%s/albums/%s?page=%d", s.baseURL, albumID, page)
		var result albumPage
		// Pages are fetched 8 at a time: a 429/5xx blip must not drop a
		// whole page of the album.
		err := netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, s.sleeper, func() error {
			result = albumPage{}
			err := netutil.GetJSON(ctx, s.client, url, s.userAgent, &result)
			var statusErr *netutil.HTTPStatusError
			if errors.As(err, &statusErr) && !model.IsRetryableStatus(statusErr.StatusCode) {
				return model.NewNonRetryable(err)
			}
			return err
		})
		if err != nil {
			return pageResult{index: page - 1, err: err, fetched: true}
		}

		pageItems := make([]model.MediaItem, 0, len(result.Images))
		var pageUnsafeErrs []error
		for _, img := range result.Images {
			if img.OriginalURL == "" {
				continue
			}
			if err := netutil.NewPolicy().ValidateURL(img.OriginalURL); err != nil {
				pageUnsafeErrs = append(pageUnsafeErrs, fmt.Errorf("unsafe image URL: %w", err))
				continue
			}
			title := img.OriginalFilename
			pageItems = append(pageItems, model.MediaItem{
				URL:      img.OriginalURL,
				Referer:  siteOrigin,
				Title:    title,
				Size:     img.SizeBytes,
				MimeType: img.Mime,
				Type:     model.MediaTypeForName(title),
			})
		}
		return pageResult{
			index:      page - 1,
			items:      pageItems,
			unsafeErrs: pageUnsafeErrs,
			current:    result.Pagination.CurrentPage,
			last:       result.Pagination.LastPage,
			fetched:    true,
		}
	}

	first := fetchPage(1)
	if first.err != nil {
		return nil, first.err
	}
	lastPage := first.last
	if lastPage < first.current {
		lastPage = first.current
	}
	if lastPage < 1 {
		lastPage = 1
	}
	if lastPage > maxAlbumPages {
		lastPage = maxAlbumPages
	}
	results := make([]pageResult, lastPage)
	results[0] = first

	const workerCount = 8
	jobs := make(chan int, workerCount)
	resultCh := make(chan pageResult, workerCount)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for page := range jobs {
				resultCh <- fetchPage(page)
			}
		}()
	}

	nextPage := 2
	pending := 0
	schedule := func() {
		for nextPage <= lastPage && pending < workerCount {
			jobs <- nextPage
			nextPage++
			pending++
		}
	}
	schedule()
	for pending > 0 {
		result := <-resultCh
		pending--
		// lastPage only ever grows. A page that under-reports last_page
		// (a flaky reply echoing last=current) must not silently drop
		// the tail of the album: pages beyond the real end simply come
		// back empty.
		if result.last > lastPage {
			lastPage = result.last
			if lastPage > maxAlbumPages {
				lastPage = maxAlbumPages
			}
		}
		if result.index >= 0 {
			if result.index >= len(results) {
				results = append(results, make([]pageResult, result.index-len(results)+1)...)
			}
			results[result.index] = result
		}
		schedule()
	}
	close(jobs)
	wg.Wait()

	var items []model.MediaItem
	var unsafeErrs []error
	var pageErrs []error
	for _, result := range results {
		// Entries never fetched (the maxAlbumPages cap stopped
		// scheduling) are zero-value slots and contribute nothing.
		if !result.fetched {
			continue
		}
		if result.err != nil {
			if errors.Is(result.err, context.Canceled) {
				return items, result.err
			}
			pageErrs = append(pageErrs, result.err)
			continue
		}
		for _, item := range result.items {
			if item.Title == "" {
				item.Title = fmt.Sprintf("goonbox_%d", len(items)+1)
			}
			items = append(items, item)
		}
		unsafeErrs = append(unsafeErrs, result.unsafeErrs...)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("goonbox: no images found in album %s", albumID)
	}

	return items, model.NewPartialError("goonbox album", append(unsafeErrs, pageErrs...))
}

func extractAlbumID(rawURL string) string {
	if idx := strings.IndexByte(rawURL, '?'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	for i, p := range parts {
		if p == "a" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	last := parts[len(parts)-1]
	if last != "" && last != "a" {
		return last
	}
	return ""
}

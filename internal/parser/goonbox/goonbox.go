// internal/parser/goonbox/goonbox.go
package goonbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

const apiBase = "https://goonbox.cr/api"

// maxAlbumPages bounds pagination: far above any real album, so only a buggy or hostile API ever hits it.
const maxAlbumPages = 1000

type Scraper struct {
	client    *http.Client
	userAgent string
	baseURL   string
}

type pageResult struct {
	index      int
	items      []model.MediaItem
	unsafeErrs []error
	current    int
	last       int
	err        error
}

func NewGoonBoxScraper() *Scraper {
	client, _ := netutil.NewPublicClient(30 * time.Second)
	return &Scraper{
		client:    client,
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   apiBase,
	}
}

// NewGoonBoxScraperWithProxy creates a GoonBox scraper that routes all
// HTTP requests through the given proxy URL.
func NewGoonBoxScraperWithProxy(proxyURL string) (*Scraper, error) {
	client, err := netutil.NewPublicClientWithProxy(30*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return &Scraper{
		client:    client,
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		baseURL:   apiBase,
	}, nil
}

func (s *Scraper) SetCookies(cookies []*http.Cookie) {
	s.client = netutil.WithCookies(s.client, cookies)
}

func NewGoonBoxScraperWithCookies(cookies []*http.Cookie) *Scraper {
	s := NewGoonBoxScraper()
	s.SetCookies(cookies)
	return s
}

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
		var result struct {
			Album struct {
				Title string `json:"title"`
			} `json:"album"`
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

		if err := netutil.GetJSON(ctx, s.client, url, s.userAgent, &result); err != nil {
			return pageResult{index: page - 1, err: err}
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
		if result.last > lastPage {
			lastPage = result.last
			if lastPage > maxAlbumPages {
				lastPage = maxAlbumPages
			}
		}
		if result.current >= result.last && result.last > 0 && result.current < lastPage {
			lastPage = result.current
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
		if result.current >= result.last && result.last > 0 {
			break
		}
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

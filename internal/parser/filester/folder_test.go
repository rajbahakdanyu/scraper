package filester

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// TestParseFilesterFolder exercises lazy folder parsing: items are built
// from the listing metadata with zero token-exchange requests, and each
// item's Resolve func performs the signed-token exchange at download time.
func TestParseFilesterFolder(t *testing.T) {
	var tokenRequests int32

	mux := http.NewServeMux()

	html := `<html><body>
		<div class="file-item" data-name="video1.mp4" data-size="1000" onclick="window.location.href='/d/slugA'"></div>
		<div class="file-item" data-name="image1.jpg" data-size="2000" onclick="window.location.href='/d/slugB'"></div>
	</body></html>`

	mux.HandleFunc("/f/testid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	})

	var ts *httptest.Server
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenRequests, 1)
		downloadTokenHandler(ts.URL, true)(w, r)
	})

	ts = httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/testid")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "video1.mp4" {
		t.Errorf("expected title video1.mp4, got %s", items[0].Title)
	}
	if items[0].Size != 1000 {
		t.Errorf("expected size 1000, got %d", items[0].Size)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}

	if items[1].Title != "image1.jpg" {
		t.Errorf("expected title image1.jpg, got %s", items[1].Title)
	}
	if items[1].Type != model.MediaTypeImage {
		t.Errorf("expected image type, got %s", items[1].Type)
	}

	// The signed download token is NOT exchanged during Parse — items
	// carry the stable file page URL and a resolver instead.
	if got := atomic.LoadInt32(&tokenRequests); got != 0 {
		t.Errorf("Parse exchanged %d download tokens eagerly; resolution must be lazy", got)
	}
	if items[0].URL != ts.URL+"/d/slugA" {
		t.Errorf("expected the stable page URL until download time, got %s", items[0].URL)
	}
	if items[0].Resolve == nil {
		t.Fatal("expected a Resolve func for lazy token resolution")
	}

	resolved, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	wantURL := fmt.Sprintf("%s/v2/file-slugA?download=true&n=video1.mp4&token=tok-slugA", ts.URL)
	if resolved != wantURL {
		t.Errorf("expected resolved URL %s, got %s", wantURL, resolved)
	}
	if got := atomic.LoadInt32(&tokenRequests); got != 1 {
		t.Errorf("expected 1 token request after Resolve, got %d", got)
	}
}

// TestParseFilesterFolderResolverClassifiesPermanentErrors keeps the
// permanent-vs-transient classification when resolution moves into the
// lazy resolver: a 404 from the token endpoint (the file is gone) is not
// retried, and surfaces as a model.NonRetryableError so the downloader fails
// the item immediately instead of retrying with backoff.
func TestParseFilesterFolderResolverClassifiesPermanentErrors(t *testing.T) {
	var tokenRequests int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/gone", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
			<div class="file-item" data-name="gone.bin" data-size="10" onclick="window.location.href='/d/slugGone'"></div>
		</body></html>`))
	})
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenRequests, 1)
		w.WriteHeader(http.StatusNotFound)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/gone")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	_, err = items[0].Resolve(context.Background())
	if err == nil {
		t.Fatal("expected resolver error for a deleted file")
	}
	var pe *model.NonRetryableError
	if !errors.As(err, &pe) {
		t.Errorf("expected a model.NonRetryableError so the downloader fails fast, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&tokenRequests); got != 1 {
		t.Errorf("permanent 404 must not be retried, got %d token requests", got)
	}
}

// TestParseFilesterFolderResolverLeavesRetryToDownloader: a lazy resolver
// makes one token request per call and reports a transient 500 as
// retryable (not permanent); the downloader's own retry calls it again.
func TestParseFilesterFolderResolverLeavesRetryToDownloader(t *testing.T) {
	var tokenRequests int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/flaky", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
			<div class="file-item" data-name="flaky.bin" data-size="10" onclick="window.location.href='/d/slugFlaky'"></div>
		</body></html>`))
	})
	var ts *httptest.Server
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&tokenRequests, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		downloadTokenHandler(ts.URL, true)(w, r)
	})

	ts = httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/flaky")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if _, err := items[0].Resolve(context.Background()); err == nil {
		t.Fatal("expected the transient 500 to surface from a single attempt")
	} else if pe := (*model.NonRetryableError)(nil); errors.As(err, &pe) {
		t.Fatalf("a 500 must stay retryable, got permanent: %v", err)
	}
	if got := atomic.LoadInt32(&tokenRequests); got != 1 {
		t.Fatalf("the resolver must not retry on its own, got %d token requests", got)
	}

	resolved, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("expected the next attempt to succeed, got: %v", err)
	}
	wantURL := fmt.Sprintf("%s/v2/file-slugFlaky?download=true&n=flaky.bin&token=tok-slugFlaky", ts.URL)
	if resolved != wantURL {
		t.Errorf("expected resolved URL %s, got %s", wantURL, resolved)
	}
	if got := atomic.LoadInt32(&tokenRequests); got != 2 {
		t.Errorf("expected 2 token requests (500 then success), got %d", got)
	}
}

func TestParseFilesterFolderPagination(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/f/multi", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p2.jpg" data-size="50" onclick="window.location.href='/d/p2'"></div>
			</body></html>`))
			return
		}
		w.Write([]byte(`<html><body>
			<button id="loadAllPagesBtn" data-total="2">Load all pages</button>
			<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
		</body></html>`))
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/multi")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items across 2 pages, got %d", len(items))
	}
	if items[0].Title != "p1.jpg" || items[1].Title != "p2.jpg" {
		t.Errorf("expected p1.jpg then p2.jpg, got %s then %s", items[0].Title, items[1].Title)
	}
}

// TestParseFilesterFolderPaginationCapped bounds pagination: a hostile or
// buggy page advertising an enormous data-total must not drive an
// effectively infinite sequential fetch loop. Like goonbox's maxAlbumPages
// cap, the total is clamped (silently) to maxFolderPages, so at most that
// many pages are ever requested.
func TestParseFilesterFolderPaginationCapped(t *testing.T) {
	var pageRequests int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/hostile", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pageRequests, 1)
		w.Header().Set("Content-Type", "text/html")
		// Same fixture shape as TestParseFilesterFolderPagination's
		// page 1, but advertising a hostile total. Serving the same
		// page for every request is fine — the point is bounding
		// the loop.
		w.Write([]byte(`<html><body>
			<button id="loadAllPagesBtn" data-total="1000000000">Load all pages</button>
			<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
		</body></html>`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	// The clamp is silent (mirroring goonbox): the parse returns the
	// pages it fetched, so only the request count is asserted here.
	_, _ = scraper.Parse(context.Background(), ts.URL+"/f/hostile")
	if got := atomic.LoadInt32(&pageRequests); got > 1001 {
		t.Errorf("pagination exceeded the 1000-page cap: %d page requests", got)
	}
}

// TestParseFilesterFolderRecoversFromTransientEmptyPage guards against a
// real bug: a page that transiently comes back with zero items (rate
// limiting, a momentary blip) used to be treated as "end of the album",
// silently truncating large albums partway through. The first attempt at
// page 2 here returns nothing; the retry should recover it.
func TestParseFilesterFolderRecoversFromTransientEmptyPage(t *testing.T) {
	var page2Attempts int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/flaky", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("page") {
		case "2":
			if atomic.AddInt32(&page2Attempts, 1) < 2 {
				w.Write([]byte(`<html><body></body></html>`)) // transient blip
				return
			}
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p2.jpg" data-size="50" onclick="window.location.href='/d/p2'"></div>
			</body></html>`))
		case "3":
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p3.jpg" data-size="60" onclick="window.location.href='/d/p3'"></div>
			</body></html>`))
		default:
			w.Write([]byte(`<html><body>
				<button id="loadAllPagesBtn" data-total="3">Load all pages</button>
				<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
			</body></html>`))
		}
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/flaky")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items (page 2 recovers on retry), got %d", len(items))
	}
}

// TestParseFilesterFolderSkipsPermanentlyEmptyPage ensures a page that
// never recovers (even after retries) is skipped rather than aborting the
// rest of the album — pages after it should still be collected.
func TestParseFilesterFolderSkipsPermanentlyEmptyPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/f/partial", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`<html><body></body></html>`)) // always empty
		case "3":
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p3.jpg" data-size="60" onclick="window.location.href='/d/p3'"></div>
			</body></html>`))
		default:
			w.Write([]byte(`<html><body>
				<button id="loadAllPagesBtn" data-total="3">Load all pages</button>
				<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
			</body></html>`))
		}
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/partial")
	var partialErr *model.PartialError
	if !errors.As(err, &partialErr) {
		t.Fatalf("expected partial-result warning, got: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items (page 3 still collected despite page 2 permanently failing), got %d", len(items))
	}
}

// TestParseFilesterFolderPermanentPageErrorNotRetried ensures that when a
// page retry hits a permanent error (a 404 can never recover), the page
// stops retrying immediately instead of burning the remaining attempts on
// backoff sleeps — and is then skipped like any other unrecoverable page.
func TestParseFilesterFolderPermanentPageErrorNotRetried(t *testing.T) {
	var page2Requests int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/gone", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("page") {
		case "2":
			if atomic.AddInt32(&page2Requests, 1) == 1 {
				w.Write([]byte(`<html><body></body></html>`)) // transiently empty
				return
			}
			w.WriteHeader(http.StatusNotFound) // permanent: retrying can't fix this
		case "3":
			w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p3.jpg" data-size="60" onclick="window.location.href='/d/p3'"></div>
			</body></html>`))
		default:
			w.Write([]byte(`<html><body>
				<button id="loadAllPagesBtn" data-total="3">Load all pages</button>
				<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
			</body></html>`))
		}
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/gone")
	var partialErr *model.PartialError
	if !errors.As(err, &partialErr) {
		t.Fatalf("expected partial-result warning, got: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items (page 2 skipped after permanent 404), got %d", len(items))
	}
	if n := atomic.LoadInt32(&page2Requests); n != 2 {
		t.Errorf("expected exactly 2 requests for page 2 (empty once, then 404 stops retries), got %d", n)
	}
}

func TestParseFilesterFolderNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	if _, err := scraper.Parse(context.Background(), ts.URL+"/f/missing"); err == nil {
		t.Error("expected error for missing folder")
	}
}

// A 5xx/429 on page > 1 must be retried like a transient empty page, not
// returned immediately: the comment promised retries protect against rate
// limiting, but a 429 used to bypass the retry loop entirely.
func TestParseFilesterFolderRecoversFromTransientHTTPError(t *testing.T) {
	var page2Attempts int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/flaky500", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("page") {
		case "2":
			if atomic.AddInt32(&page2Attempts, 1) < 2 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p2.jpg" data-size="50" onclick="window.location.href='/d/p2'"></div>
			</body></html>`))
		case "3":
			_, _ = w.Write([]byte(`<html><body>
				<div class="file-item" data-name="p3.jpg" data-size="60" onclick="window.location.href='/d/p3'"></div>
			</body></html>`))
		default:
			_, _ = w.Write([]byte(`<html><body>
				<button id="loadAllPagesBtn" data-total="3">Load all pages</button>
				<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
			</body></html>`))
		}
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/f/flaky500")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items (page 2 recovers after 500), got %d", len(items))
	}
}

// A page that is 200-empty and then rate-limited must report the last HTTP
// error instead of a generic "returned no items".
func TestParseFilesterFolderReportsLastHTTPError(t *testing.T) {
	var page2Requests int32

	mux := http.NewServeMux()
	mux.HandleFunc("/f/ratelimited", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("page") {
		case "2":
			if atomic.AddInt32(&page2Requests, 1) == 1 {
				w.Write([]byte(`<html><body></body></html>`))
				return
			}
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.Write([]byte(`<html><body>
				<button id="loadAllPagesBtn" data-total="2">Load all pages</button>
				<div class="file-item" data-name="p1.jpg" data-size="40" onclick="window.location.href='/d/p1'"></div>
			</body></html>`))
		}
	})
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	_, err := scraper.Parse(context.Background(), ts.URL+"/f/ratelimited")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected the last HTTP error (429) in the failure, got: %v", err)
	}
}

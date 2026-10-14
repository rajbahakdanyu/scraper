package filester

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResolveFileUsesContextAwareBackoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "retry", http.StatusTooManyRequests)
	}))
	defer server.Close()

	sleeper := &resolveSleeper{}
	scraper := newTestFilesterScraper()
	scraper.baseURL = server.URL
	scraper.SetSleeper(sleeper)

	ctx := context.Background()
	_, err := scraper.resolveFile(ctx, "slug", "name")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("resolveFile error = %v, want context.Canceled", err)
	}
	if !sleeper.sleepCtxCalled {
		t.Fatal("expected resolveFile to use SleepCtx for retry backoff")
	}
}

func TestResolveFileRetriesGarbledTokenResponse(t *testing.T) {
	var calls int32
	var ts *httptest.Server // used inside the handler, assigned after construction
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = w.Write([]byte("gateway garbage")) // truncated body
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"file": "d/abc", "token": "tok", "server": ts.URL + "/", "name": "v.mp4",
		})
	})
	ts = httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.SetSleeper(noopSleeper{})
	item, err := scraper.resolveFile(context.Background(), "abc", "")
	if err != nil {
		t.Fatalf("a single garbled token response must be retried, got %v", err)
	}
	if item.URL == "" {
		t.Fatal("expected a resolved item")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("token endpoint should have been called twice, got %d", got)
	}
}

func TestParseFilesterSingleFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("", false))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/d/Sw9EGcH")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Sw9EGcH-name.bin" {
		t.Errorf("unexpected title: %s", items[0].Title)
	}
	if items[0].URL != ts.URL+"/d/Sw9EGcH" {
		t.Errorf("expected stable page URL, got %s", items[0].URL)
	}
	if strings.Contains(items[0].URL, "token=") {
		t.Errorf("single-file page URL leaked a token: %s", items[0].URL)
	}
	if items[0].Resolve == nil {
		t.Fatal("expected a resolver for the signed download URL")
	}
	wantURL := fmt.Sprintf("%s/v2/file-Sw9EGcH?download=true&n=Sw9EGcH-name.bin&token=tok-Sw9EGcH", ts.URL)
	resolved, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolved != wantURL {
		t.Errorf("expected resolved URL %s, got %s", wantURL, resolved)
	}
}

// The site hands out rotating cdn.cr shards (fsc1, fsc3, ...); the strict
// allowlist must accept the whole family, not just the filester.me list.
func TestParseFilesterSingleFileAllowsCDNFamily(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", downloadTokenHandler("https://fsc3.cdn.cr", true))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	items, err := scraper.Parse(context.Background(), ts.URL+"/d/Sw9EGcH")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	resolved, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !strings.HasPrefix(resolved, "https://fsc3.cdn.cr/v2/") {
		t.Fatalf("resolved URL = %q, want an fsc3.cdn.cr URL", resolved)
	}
}

// TestParseFilesterSingleFileReResolvesFreshTokens guards against the bug
// where the single-file resolver returned the signed URL captured at parse
// time forever: tokens expire, so a 403-triggered re-resolve must perform a
// fresh token exchange, not replay the stale signature.
func TestParseFilesterSingleFileReResolvesFreshTokens(t *testing.T) {
	var tokenRequests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&tokenRequests, 1)
		var body struct {
			FileSlug string `json:"file_slug"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		resp := map[string]string{
			"file":  "file-" + body.FileSlug,
			"token": fmt.Sprintf("tok-%s-%d", body.FileSlug, n),
			"name":  body.FileSlug + "-name.bin",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/d/Sw9EGcH")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(items) != 1 || items[0].Resolve == nil {
		t.Fatalf("expected 1 resolvable item, got %d items", len(items))
	}

	first, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	second, err := items[0].Resolve(context.Background())
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if first == second {
		t.Error("Resolve returned the same captured URL twice; expected a fresh token exchange per call")
	}
	if got := atomic.LoadInt32(&tokenRequests); got < 2 {
		t.Errorf("expected at least 2 token exchanges, got %d", got)
	}
}

func TestParseFilesterRejectsOversizedHTML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 32<<20)+`<div class="file-item" data-name="late.bin" data-size="1" onclick="window.location.href='/d/late'"></div>`)
	}))
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	items, err := scraper.Parse(context.Background(), ts.URL+"/f/oversized")
	if err == nil || len(items) != 0 {
		t.Fatalf("expected link truncated by HTML limit, items=%d err=%v", len(items), err)
	}
}

// TestParseFilesterSingleFileNotFoundNotRetried guards against the bug
// where a permanent failure from the download-token endpoint (a 404 means
// the file simply doesn't exist) was retried with exponential backoff,
// making the user wait through several sleeps just to be told the file is
// gone.
func TestParseFilesterSingleFileNotFoundNotRetried(t *testing.T) {
	var tokenRequests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenRequests, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	if _, err := scraper.Parse(context.Background(), ts.URL+"/d/missing"); err == nil {
		t.Fatal("expected error for missing file")
	}
	if n := atomic.LoadInt32(&tokenRequests); n != 1 {
		t.Errorf("expected exactly 1 token request for a permanent 404, got %d", n)
	}
}

// TestParseFilesterSingleFileMalformedTokenIsRetried ensures an
// undecodable token response is treated as transient: a gateway can
// truncate a body for reasons that resolve on the next attempt, so the
// request is retried up to maxResolveRetries and then fails.
func TestParseFilesterSingleFileMalformedTokenIsRetried(t *testing.T) {
	var tokenRequests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	if _, err := scraper.Parse(context.Background(), ts.URL+"/d/garbage"); err == nil {
		t.Fatal("expected error for malformed token response")
	}
	if n := atomic.LoadInt32(&tokenRequests); n != maxResolveRetries {
		t.Errorf("expected %d token requests for an undecodable response, got %d", maxResolveRetries, n)
	}
}

// TestParseFilesterSingleFileIncompleteTokenIsRetried ensures a
// well-formed but useless token response (missing file/token fields) is
// treated as transient and retried up to maxResolveRetries before
// failing: a 200 with an empty token is a server-side blip, not a
// property of the file.
func TestParseFilesterSingleFileIncompleteTokenIsRetried(t *testing.T) {
	var tokenRequests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"file": "", "token": ""})
	}))
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL

	if _, err := scraper.Parse(context.Background(), ts.URL+"/d/incomplete"); err == nil {
		t.Fatal("expected error for incomplete token response")
	}
	if n := atomic.LoadInt32(&tokenRequests); n != maxResolveRetries {
		t.Errorf("expected %d token requests for an incomplete token, got %d", maxResolveRetries, n)
	}
}

// TestParseFilesterSingleFileRetriesTransientServerError ensures transient
// failures keep the existing retry behavior: a 500 that recovers on the
// next attempt still succeeds.
func TestParseFilesterSingleFileRetriesTransientServerError(t *testing.T) {
	var tokenRequests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&tokenRequests, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		downloadTokenHandler("", false)(w, r)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/d/flaky")
	if err != nil {
		t.Fatalf("expected transient 500 to be retried successfully, got: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if n := atomic.LoadInt32(&tokenRequests); n != 2 {
		t.Errorf("expected 2 token requests (500 then success), got %d", n)
	}
}

// TestParseFilesterSingleFileRetriesRateLimit ensures 429 (rate limiting)
// is treated as transient and retried, same as a 5xx.
func TestParseFilesterSingleFileRetriesRateLimit(t *testing.T) {
	var tokenRequests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&tokenRequests, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		downloadTokenHandler("", false)(w, r)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	items, err := scraper.Parse(context.Background(), ts.URL+"/d/limited")
	if err != nil {
		t.Fatalf("expected transient 429 to be retried successfully, got: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if n := atomic.LoadInt32(&tokenRequests); n != 2 {
		t.Errorf("expected 2 token requests (429 then success), got %d", n)
	}
}

func TestParseFilesterUnrecognizedURL(t *testing.T) {
	scraper := newTestFilesterScraper()
	if _, err := scraper.Parse(context.Background(), "https://filester.sh/faq"); err == nil {
		t.Error("expected error for unrecognized URL format")
	}
}

func TestExtractKindAndID(t *testing.T) {
	tests := []struct {
		url      string
		wantKind string
		wantID   string
	}{
		{"https://filester.sh/f/473d3487215583bb", "f", "473d3487215583bb"},
		{"https://filester.sh/f/473d3487215583bb/", "f", "473d3487215583bb"},
		{"https://filester.me/d/L7edMh1", "d", "L7edMh1"},
		{"https://filester.sh/d/L7edMh1?utm=1", "d", "L7edMh1"},
		{"https://filester.sh/", "", ""},
		{"https://filester.sh/faq", "", ""},
	}

	for _, tt := range tests {
		kind, id := extractKindAndID(tt.url)
		if kind != tt.wantKind || id != tt.wantID {
			t.Errorf("extractKindAndID(%s) = (%s, %s), want (%s, %s)", tt.url, kind, id, tt.wantKind, tt.wantID)
		}
	}
}

func TestParseFilesterRejectsUnlistedCDNServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/api/public/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"file":   "file-x",
			"token":  "tok",
			"server": "https://evil.example.net",
			"name":   "x.bin",
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := newTestFilesterScraper()
	scraper.baseURL = ts.URL
	scraper.cdnURLs = []string{ts.URL}

	item, err := scraper.resolveFile(context.Background(), "slug", "x.bin")
	_ = item
	if err == nil {
		t.Fatal("expected an unlisted CDN server to be rejected")
	}
	if !strings.Contains(err.Error(), "unsafe CDN server") {
		t.Fatalf("error = %v, want unsafe CDN server", err)
	}
}

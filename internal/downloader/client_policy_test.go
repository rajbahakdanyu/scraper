package downloader

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

// newPolicyTestDownloader keeps the production client policy (public-IP
// dial guard, redirect checks, cookie jar) but allowlists the test server,
// unlike newTestDownloader's plain http.Client.
func newPolicyTestDownloader(t *testing.T, serverURL string, cookies []*http.Cookie) *Downloader {
	t.Helper()
	dl, err := NewDownloaderWithConfig(1, time.Second, "PolicyTest/1.0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := netutil.NewPublicStreamingClientWithConcurrency(1, time.Second, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	dl.client = netutil.WithCookies(client, cookies)
	dl.SetSleeper(noopSleeper{})
	return dl
}

func TestProductionDownloaderRefusesLoopback(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer ts.Close()

	dl, err := NewDownloaderWithConfig(1, time.Second, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	dl.SetSleeper(noopSleeper{})
	if _, err := dl.Download(model.MediaItem{URL: ts.URL + "/a.bin", Title: "a.bin"}, t.TempDir()); err == nil {
		t.Fatal("the production client must refuse a loopback download")
	}
	if requests.Load() != 0 {
		t.Fatalf("loopback server was reached %d times", requests.Load())
	}
}

func TestRealClientSendsUserAgentAndCookies(t *testing.T) {
	var gotUA, gotCookie string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.UserAgent()
		if c, err := r.Cookie("sid"); err == nil {
			gotCookie = c.Value
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	dl := newPolicyTestDownloader(t, ts.URL, []*http.Cookie{{Name: "sid", Value: "secret", Domain: u.Hostname(), Path: "/"}})
	if _, err := dl.Download(model.MediaItem{URL: ts.URL + "/a.bin", Title: "a.bin"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if gotUA != "PolicyTest/1.0" || gotCookie != "secret" {
		t.Fatalf("server saw UA %q cookie %q, want PolicyTest/1.0 and secret", gotUA, gotCookie)
	}
}

func TestRealClientRefusesRedirectToNonPublicHost(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()
	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:"+targetURL.Port()+"/internal", http.StatusFound)
	}))
	defer ts.Close()

	dl := newPolicyTestDownloader(t, ts.URL, nil)
	if _, err := dl.Download(model.MediaItem{URL: ts.URL + "/a.bin", Title: "a.bin"}, t.TempDir()); err == nil {
		t.Fatal("a redirect to localhost must be refused")
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target was reached %d times", targetHits.Load())
	}
}

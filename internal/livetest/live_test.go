//go:build live

// Package livetest checks every supported site against the real internet:
// each site's scraper must parse a known URL and the downloader must fetch
// one item from it. It only builds with -tags live and only tests sites
// whose LIVE_URL_<SITE> variable is set, so it never runs by accident.
//
//	LIVE_URL_PIXELDRAIN=https://pixeldrain.com/u/abc go test -tags live -v ./internal/livetest
package livetest

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

const defaultMaxBytes = 200 << 20

func TestLiveSites(t *testing.T) {
	maxBytes := int64(defaultMaxBytes)
	if v := os.Getenv("LIVE_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			t.Fatalf("LIVE_MAX_BYTES=%q is not a positive byte count", v)
		}
		maxBytes = n
	}

	configured := 0
	for _, site := range app.Sites() {
		urls := strings.Fields(strings.ReplaceAll(os.Getenv(envName(site)), ",", " "))
		configured += len(urls)
		for _, rawURL := range urls {
			t.Run(site, func(t *testing.T) {
				checkSite(t, site, rawURL, maxBytes)
			})
		}
	}
	if configured == 0 {
		t.Skip("no LIVE_URL_<SITE> variables set; nothing to check")
	}
}

func envName(site string) string {
	return "LIVE_URL_" + strings.ToUpper(site)
}

func checkSite(t *testing.T, site, rawURL string, maxBytes int64) {
	detected, err := app.DetectSite(rawURL)
	if err != nil || detected != site {
		t.Fatalf("DetectSite(%s) = %q, %v; want %q", rawURL, detected, err, site)
	}

	// An empty explicit config: defaults, never the developer's own config.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if site == "youtube" {
		cfg.Quality = "360p"
	}
	if err := app.FinalizeConfig(cfg); err != nil {
		t.Fatal(err)
	}
	scraper, err := app.NewScraper(site, cfg.Proxy, cfg.ParsedCookies())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	items, err := scraper.Parse(ctx, rawURL)
	if err != nil {
		t.Fatalf("parse %s: %v", rawURL, err)
	}
	if len(items) == 0 {
		t.Fatalf("parse %s found no items", rawURL)
	}
	t.Logf("parsed %d item(s)", len(items))
	if site == "youtube" {
		// The reported size is the best format's; the 360p cap bounds it.
		maxBytes = 1<<63 - 1
	}
	item, ok := pickItem(items, maxBytes)
	if !ok {
		t.Skipf("every item is larger than LIVE_MAX_BYTES (%d)", maxBytes)
	}

	dl, err := app.BuildDownloader(scraper, site, cfg, app.Hooks{Notice: func(msg string) { t.Log(msg) }})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range dl.Progress() {
		}
	}()
	defer func() {
		dl.Shutdown()
		<-drained
	}()

	start := time.Now()
	path, err := dl.Download(item, t.TempDir())
	if err != nil {
		t.Fatalf("download %q (%s): %v", item.Title, item.URL, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatalf("downloaded %s is empty", path)
	}
	// YouTube sizes are per stream, not per muxed file.
	if site != "youtube" && item.Size > 0 && info.Size() != item.Size {
		t.Fatalf("downloaded %d bytes, the site reported %d", info.Size(), item.Size)
	}
	t.Logf("downloaded %q: %d bytes in %s", filepath.Base(path), info.Size(), time.Since(start).Round(time.Millisecond))
}

// pickItem prefers the smallest item with a known size under the cap, so a
// big album doesn't make the check slow; unknown sizes are a last resort.
func pickItem(items []model.MediaItem, maxBytes int64) (model.MediaItem, bool) {
	known := slices.DeleteFunc(slices.Clone(items), func(it model.MediaItem) bool {
		return it.Size <= 0 || it.Size > maxBytes
	})
	if len(known) > 0 {
		return slices.MinFunc(known, func(a, b model.MediaItem) int { return cmp.Compare(a.Size, b.Size) }), true
	}
	for _, it := range items {
		if it.Size <= 0 {
			return it, true
		}
	}
	return model.MediaItem{}, false
}

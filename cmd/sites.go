package cmd

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
	"github.com/rajbahakdanyu/scraper/internal/parser/bunker"
	"github.com/rajbahakdanyu/scraper/internal/parser/cyberdrop"
	"github.com/rajbahakdanyu/scraper/internal/parser/filester"
	"github.com/rajbahakdanyu/scraper/internal/parser/goonbox"
	"github.com/rajbahakdanyu/scraper/internal/parser/pixeldrain"
	"github.com/rajbahakdanyu/scraper/internal/parser/turbo"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
)

// siteScrapers builds each supported site's scraper with the run's proxy
// and cookies.
var siteScrapers = map[string]func(proxyURL string, cookies []*http.Cookie) (model.Scraper, error){
	"youtube": func(p string, c []*http.Cookie) (model.Scraper, error) {
		return youtube.NewYouTubeScraperWithOptions(p, c)
	},
	"cyberdrop": func(p string, c []*http.Cookie) (model.Scraper, error) {
		return cyberdrop.NewCyberDropScraperWithOptions(p, c)
	},
	"bunker": func(p string, c []*http.Cookie) (model.Scraper, error) {
		return bunker.NewBunkerScraperWithOptions(p, c)
	},
	"goonbox": func(p string, c []*http.Cookie) (model.Scraper, error) {
		return goonbox.NewGoonBoxScraperWithOptions(p, c)
	},
	"filester": func(p string, c []*http.Cookie) (model.Scraper, error) {
		return filester.NewFilesterScraperWithOptions(p, c)
	},
	"pixeldrain": func(p string, c []*http.Cookie) (model.Scraper, error) {
		return pixeldrain.NewPixeldrainScraperWithOptions(p, c)
	},
	"turbo": func(p string, c []*http.Cookie) (model.Scraper, error) {
		return turbo.NewTurboScraperWithOptions(p, c)
	},
}

// newSiteScraper builds the scraper for one site.
func newSiteScraper(site, proxyURL string, cookies []*http.Cookie) (model.Scraper, error) {
	newScraper, ok := siteScrapers[site]
	if !ok {
		return nil, fmt.Errorf("unknown site %q", site)
	}
	return newScraper(proxyURL, cookies)
}

// scraperRegistry builds one scraper per supported site, for batch runs
// whose URLs can span several sites.
func scraperRegistry(proxyURL string, cookies []*http.Cookie) (map[string]model.Scraper, error) {
	reg := make(map[string]model.Scraper, len(siteScrapers))
	for site := range siteScrapers {
		scraper, err := newSiteScraper(site, proxyURL, cookies)
		if err != nil {
			return nil, err
		}
		reg[site] = scraper
	}
	return reg, nil
}

// resolveURL extracts the target URL from positional args or the --url flag.
func resolveURL(args []string, urlFlag, siteName string) (string, error) {
	targetURL := urlFlag
	if len(args) > 0 {
		targetURL = args[0]
	}
	if targetURL == "" {
		return "", fmt.Errorf("a %s URL is required (positional argument or --url)", siteName)
	}
	return targetURL, nil
}

// albumSlugFor picks the subfolder name for a multi-item download. YouTube
// playlist URLs don't have a unique path segment (they're all just
// "/playlist?list=..."), so the generic "last path segment" logic would
// put every playlist in the same "playlist" folder — use the
// playlist ID instead for those. Plain video URLs all share the "watch"
// path, so fall back to the video ID for those too.
func albumSlugFor(siteName, targetURL string) string {
	if siteName == "youtube" {
		if playlistID := youtube.ExtractPlaylistID(targetURL); playlistID != "" {
			return playlistID
		}
		if videoID := youtube.ExtractVideoID(targetURL); videoID != "" {
			return videoID
		}
	}
	return netutil.PathSlug(targetURL)
}

// siteDomains maps each supported scraper to the known domains it operates
// under. Cyberlocker-style sites (Bunkr, CyberDrop, GoonBox) rotate through
// many mirror domains over time, usually in response to takedowns, so this
// list is necessarily incomplete — add new mirrors here as they show up.
var siteDomains = map[string][]string{
	"youtube": {"youtube.com", "youtu.be"},
	"cyberdrop": {
		"cyberdrop.cr", "cyberdrop.me", "cyberdrop.to", "cyberdrop.cc",
	},
	"bunker":     bunker.MirrorDomains(),
	"goonbox":    {"goonbox.cr", "goonbox.to"},
	"filester":   {"filester.sh", "filester.me"},
	"pixeldrain": {"pixeldrain.com"},
	"turbo":      {"turbo.cr"},
}

// detectSite guesses which scraper module handles rawURL based on its host,
// matching against siteDomains rather than a plain substring so a lookalike
// host (e.g. "notcyberdrop.example.com") can't be mistaken for the real site.
func detectSite(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("could not parse %q as a URL", rawURL)
	}
	host := strings.ToLower(u.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	for site, domains := range siteDomains {
		for _, domain := range domains {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return site, nil
			}
		}
	}
	return "", fmt.Errorf("could not auto-detect a site for %q (available: youtube, cyberdrop, bunker, goonbox, filester, pixeldrain, turbo)", rawURL)
}

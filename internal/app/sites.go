// Package app holds the frontend-neutral use cases shared by the CLI/TUI
// and the GUI: site detection, config finalization, filtering, previews,
// downloader construction, and retry reports.
package app

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
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

// Sites returns the supported site names, sorted.
func Sites() []string {
	sites := make([]string, 0, len(siteScrapers))
	for site := range siteScrapers {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	return sites
}

// NewScraper builds the scraper for one site.
func NewScraper(site, proxyURL string, cookies []*http.Cookie) (model.Scraper, error) {
	newScraper, ok := siteScrapers[site]
	if !ok {
		return nil, fmt.Errorf("unknown site %q", site)
	}
	return newScraper(proxyURL, cookies)
}

// Registry builds one scraper per supported site, for batch runs whose URLs
// can span several sites.
func Registry(proxyURL string, cookies []*http.Cookie) (map[string]model.Scraper, error) {
	reg := make(map[string]model.Scraper, len(siteScrapers))
	for site := range siteScrapers {
		scraper, err := NewScraper(site, proxyURL, cookies)
		if err != nil {
			return nil, err
		}
		reg[site] = scraper
	}
	return reg, nil
}

// AlbumSlug picks the subfolder name for a multi-item download. YouTube
// playlist URLs don't have a unique path segment (they're all just
// "/playlist?list=..."), so the generic "last path segment" logic would
// put every playlist in the same "playlist" folder — use the
// playlist ID instead for those. Plain video URLs all share the "watch"
// path, so fall back to the video ID for those too.
func AlbumSlug(siteName, targetURL string) string {
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

// DestSlug picks the destination folder for a batch entry: the album's slug
// when the entry carries an album URL (a retry pair), else the entry's own
// slug.
func DestSlug(siteName string, e Entry) string {
	if e.AlbumURL != "" {
		return AlbumSlug(siteName, e.AlbumURL)
	}
	return AlbumSlug(siteName, e.URL)
}

// SiteDomains maps each supported scraper to the known domains it operates
// under. Cyberlocker-style sites (Bunkr, CyberDrop, GoonBox) rotate through
// many mirror domains over time, usually in response to takedowns, so this
// list is necessarily incomplete — add new mirrors here as they show up.
var SiteDomains = map[string][]string{
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

// AllDomains flattens SiteDomains.
func AllDomains() []string {
	var all []string
	for _, site := range Sites() {
		all = append(all, SiteDomains[site]...)
	}
	return all
}

// DetectSite guesses which scraper module handles rawURL based on its host,
// matching against SiteDomains rather than a plain substring so a lookalike
// host (e.g. "notcyberdrop.example.com") can't be mistaken for the real site.
func DetectSite(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("could not parse %q as a URL", rawURL)
	}
	host := strings.ToLower(u.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	for site, domains := range SiteDomains {
		for _, domain := range domains {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return site, nil
			}
		}
	}
	return "", fmt.Errorf("could not auto-detect a site for %q (available: %s)", rawURL, strings.Join(Sites(), ", "))
}

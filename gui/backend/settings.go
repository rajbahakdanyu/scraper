package backend

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
)

// Settings mirrors every config-file key in a form-friendly shape:
// durations are strings ("500ms") and lists are plain arrays.
type Settings struct {
	OutputDir          string   `json:"outputDir"`
	Concurrency        int      `json:"concurrency"`
	PerHost            int      `json:"perHost"`
	RateLimit          string   `json:"rateLimit"`
	Checksums          bool     `json:"checksums"`
	Proxy              string   `json:"proxy"`
	Cookies            string   `json:"cookies"`
	CookiesFromBrowser string   `json:"cookiesFromBrowser"`
	Quality            string   `json:"quality"`
	YouTubeBackend     string   `json:"youtubeBackend"`
	AudioOnly          string   `json:"audioOnly"`
	Include            []string `json:"include"`
	Exclude            []string `json:"exclude"`
	MediaExtensions    []string `json:"mediaExtensions"`
	MinSize            string   `json:"minSize"`
	MaxSize            string   `json:"maxSize"`
	MaxAlbumItems      int      `json:"maxAlbumItems"`
	ScrapeDelay        string   `json:"scrapeDelay"`
	Timeout            string   `json:"timeout"`
	UserAgent          string   `json:"userAgent"`
	LogFile            string   `json:"logFile"`
	DiskCheck          bool     `json:"diskCheck"`
	MediaOnly          bool     `json:"mediaOnly"`
	Notify             bool     `json:"notify"`
}

// Options lists the fixed choices the settings form offers.
type Options struct {
	Sites        []string `json:"sites"`
	Qualities    []string `json:"qualities"`
	AudioFormats []string `json:"audioFormats"`
	Browsers     []string `json:"browsers"`
	ConfigPath   string   `json:"configPath"`
}

func settingsFromConfig(c *config.Config) Settings {
	return Settings{
		OutputDir:          c.OutputDir,
		Concurrency:        c.ConcurrentDownloads,
		PerHost:            c.PerHostConcurrency,
		RateLimit:          c.RateLimit,
		Checksums:          c.Checksums,
		Proxy:              c.Proxy,
		Cookies:            c.Cookies,
		CookiesFromBrowser: c.CookiesFromBrowser,
		Quality:            c.Quality,
		YouTubeBackend:     guiBackend(c.YouTubeBackend),
		AudioOnly:          c.AudioOnly,
		Include:            nonNil(c.Include),
		Exclude:            nonNil(c.Exclude),
		MediaExtensions:    nonNil(c.MediaExtensions),
		MinSize:            c.MinSize,
		MaxSize:            c.MaxSize,
		MaxAlbumItems:      c.MaxAlbumItems,
		ScrapeDelay:        durationText(c.ScrapeDelay),
		Timeout:            durationText(c.Timeout),
		UserAgent:          c.UserAgent,
		LogFile:            c.LogFile,
		DiskCheck:          c.DiskCheck,
		MediaOnly:          c.MediaOnly,
		Notify:             c.Notify,
	}
}

// apply copies s onto c, failing on unparsable durations.
func (s Settings) apply(c *config.Config) error {
	scrapeDelay, err := parseDuration("scrape delay", s.ScrapeDelay)
	if err != nil {
		return err
	}
	timeout, err := parseDuration("timeout", s.Timeout)
	if err != nil {
		return err
	}
	c.OutputDir = strings.TrimSpace(s.OutputDir)
	c.ConcurrentDownloads = s.Concurrency
	c.PerHostConcurrency = s.PerHost
	c.RateLimit = strings.TrimSpace(s.RateLimit)
	c.Checksums = s.Checksums
	c.Proxy = strings.TrimSpace(s.Proxy)
	c.Cookies = strings.TrimSpace(s.Cookies)
	c.CookiesFromBrowser = s.CookiesFromBrowser
	c.Quality = s.Quality
	c.YouTubeBackend = s.YouTubeBackend
	c.AudioOnly = s.AudioOnly
	c.Include = cleanList(s.Include)
	c.Exclude = cleanList(s.Exclude)
	c.MediaExtensions = cleanList(s.MediaExtensions)
	c.MinSize = strings.TrimSpace(s.MinSize)
	c.MaxSize = strings.TrimSpace(s.MaxSize)
	c.MaxAlbumItems = s.MaxAlbumItems
	c.ScrapeDelay = scrapeDelay
	c.Timeout = timeout
	c.UserAgent = strings.TrimSpace(s.UserAgent)
	c.LogFile = strings.TrimSpace(s.LogFile)
	c.DiskCheck = s.DiskCheck
	c.MediaOnly = s.MediaOnly
	c.Notify = s.Notify
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.UserAgent == "" {
		c.UserAgent = config.DefaultUserAgent
	}
	return nil
}

// validate checks s the way a run would, without importing browser
// cookies (that reads the browser's database and runs at run start).
func (s Settings) validate(base *config.Config) error {
	c := *base
	if err := s.apply(&c); err != nil {
		return err
	}
	if c.OutputDir == "" {
		return errors.New("output folder is required")
	}
	if c.CookiesFromBrowser != "" && c.CookiesFromBrowser != "firefox" {
		return fmt.Errorf("unsupported browser %q (only firefox is supported)", c.CookiesFromBrowser)
	}
	c.CookiesFromBrowser = ""
	if _, err := config.ParseRateLimit(c.RateLimit); err != nil {
		return err
	}
	return app.FinalizeConfig(&c)
}

func parseDuration(name, text string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid %s %q (e.g. 500ms, 2s)", name, text)
	}
	return d, nil
}

func durationText(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// guiBackend maps the explicit "auto" backend to "", the GUI's auto option.
func guiBackend(backend string) string {
	if backend == "auto" {
		return ""
	}
	return backend
}

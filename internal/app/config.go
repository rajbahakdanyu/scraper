package app

import (
	"fmt"
	"path/filepath"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/cookies"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
)

// Qualities lists the accepted YouTube quality caps, best first.
var Qualities = []string{"2160p", "1440p", "1080p", "720p", "480p", "360p", "240p"}

// AudioFormats lists the accepted YouTube audio-only formats.
var AudioFormats = []string{"m4a", "mp3"}

// importBrowserCookies is a test seam for the browser cookie importer.
var importBrowserCookies = cookies.Import

// FinalizeConfig validates cfg after every override is applied, parses its
// derived values (size bounds, cookies) and expands ~ in the output dir.
// Opening the log file is left to the caller.
func FinalizeConfig(cfg *config.Config) error {
	if cfg.ConcurrentDownloads < 1 || cfg.ConcurrentDownloads > 256 {
		return fmt.Errorf("invalid --concurrent %d (must be between 1 and 256)", cfg.ConcurrentDownloads)
	}
	if cfg.Quality != "" && cfg.AudioOnly != "" {
		return fmt.Errorf("cannot use --quality and --audio-only together")
	}
	if cfg.Quality != "" {
		if _, ok := youtube.ValidQuality(cfg.Quality); !ok {
			return fmt.Errorf("invalid --quality value %q (valid: 2160p, 1440p, 1080p, 720p, 480p, 360p, 240p)", cfg.Quality)
		}
	}
	if cfg.AudioOnly != "" {
		if cfg.AudioOnly != "m4a" && cfg.AudioOnly != "mp3" {
			return fmt.Errorf("invalid --audio-only value %q (valid: m4a, mp3)", cfg.AudioOnly)
		}
	}

	for i, p := range cfg.Include {
		if _, err := filepath.Match(p, "test"); err != nil {
			return fmt.Errorf("invalid --include pattern %q (position %d): %w", p, i+1, err)
		}
	}
	for i, p := range cfg.Exclude {
		if _, err := filepath.Match(p, "test"); err != nil {
			return fmt.Errorf("invalid --exclude pattern %q (position %d): %w", p, i+1, err)
		}
	}

	cfg.SetMinSizeBytes(0)
	if cfg.MinSize != "" {
		n, err := config.ParseSize(cfg.MinSize)
		if err != nil {
			return fmt.Errorf("invalid --min-size: %w", err)
		}
		cfg.SetMinSizeBytes(n)
	}
	cfg.SetMaxSizeBytes(0)
	if cfg.MaxSize != "" {
		n, err := config.ParseSize(cfg.MaxSize)
		if err != nil {
			return fmt.Errorf("invalid --max-size: %w", err)
		}
		cfg.SetMaxSizeBytes(n)
	}
	cfg.SetParsedCookies(nil)
	if cfg.Cookies != "" {
		expandedCookies, err := config.ExpandHomePath(cfg.Cookies)
		if err != nil {
			return err
		}
		cfg.Cookies = expandedCookies
		parsed, err := netutil.ParseNetscapeCookies(cfg.Cookies)
		if err != nil {
			return fmt.Errorf("cookies: %w", err)
		}
		cfg.SetParsedCookies(parsed)
	}
	if cfg.CookiesFromBrowser != "" {
		imported, err := importBrowserCookies(cfg.CookiesFromBrowser, AllDomains())
		if err != nil {
			return err
		}
		cfg.SetParsedCookies(append(cfg.ParsedCookies(), imported...))
	}

	// Single choke point covering both the --output flag and the config
	// file's output_dir: a flag override bypasses config.Load's own
	// expansion, and the shell never expands a quoted `-o "~"` value.
	outputDir, err := config.ExpandHomePath(cfg.OutputDir)
	if err != nil {
		return err
	}
	cfg.OutputDir = outputDir
	return nil
}

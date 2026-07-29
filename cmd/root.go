package cmd

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/config"
	"github.com/user/scraper/internal/downloader"
	"github.com/user/scraper/internal/model"
	"github.com/user/scraper/internal/parser/bunker"
	"github.com/user/scraper/internal/parser/cyberdrop"
	"github.com/user/scraper/internal/parser/filester"
	"github.com/user/scraper/internal/parser/goonbox"
	"github.com/user/scraper/internal/parser/youtube"
	"github.com/user/scraper/internal/ui/dashboard"
)

// globalOptions holds flags defined on the root command that are inherited
// by all subcommands via PersistentFlags.
type globalOptions struct {
	configPath  string
	output      string
	concurrency int
}

// loadGlobalConfig loads the config file and applies CLI flag overrides.
func loadGlobalConfig(opts globalOptions) *config.Config {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		cfg = &config.Config{
			OutputDir:           ".",
			ConcurrentDownloads: 5,
			UserAgent:           "Scraper/1.0",
		}
	}

	if opts.output != "" && opts.output != "." {
		cfg.OutputDir = opts.output
	}
	if opts.concurrency > 0 && opts.concurrency != 5 {
		cfg.ConcurrentDownloads = opts.concurrency
	}

	return cfg
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func NewRootCmd() *cobra.Command {
	opts := globalOptions{}

	root := &cobra.Command{
		Use:   "scraper [url]",
		Short: "Media scraper CLI tool",
		Long:  `Download media from various sites with a beautiful terminal UI.`,
		// Runtime errors (bad URL, network failure, etc.) shouldn't dump the
		// full usage/help text — just cobra's own clean "Error: ..." line.
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			targetURL := args[0]
			site, err := detectSite(targetURL)
			if err != nil {
				return err
			}
			return runWithScraper(scraperRegistry()[site], targetURL, site, opts)
		},
	}

	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "Config file path (default: ~/.scraper.yaml)")
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", ".", "Output directory")
	root.PersistentFlags().IntVar(&opts.concurrency, "concurrent", 5, "Maximum concurrent downloads")

	root.AddCommand(
		newYouTubeCmd(opts),
		newCyberDropCmd(opts),
		newBunkerCmd(opts),
		newGoonBoxCmd(opts),
		newFilesterCmd(opts),
	)

	return root
}

// scraperRegistry maps a site name to its scraper, used by detectSite's
// auto-detect path.
func scraperRegistry() map[string]model.Scraper {
	return map[string]model.Scraper{
		"youtube":   youtube.NewYouTubeScraper(),
		"cyberdrop": cyberdrop.NewCyberDropScraper(),
		"bunker":    bunker.NewBunkerScraper(),
		"goonbox":   goonbox.NewGoonBoxScraper(),
		"filester":  filester.NewFilesterScraper(),
	}
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

// extractSlug gets the last path segment from a URL, used for naming
// album subfolders.
func extractSlug(rawURL string) string {
	// Remove query string
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if last == "" {
		return ""
	}
	return last
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
	"bunker": {
		"bunkr.cr", "bunkr.su", "bunkrr.su", "bunkr.si", "bunkr.la",
		"bunkr.is", "bunkr.to", "bunkr.ru", "bunkr.black", "bunkr.red",
		"bunkr.ph", "bunkr.site", "bunkr.fi",
	},
	"goonbox":  {"goonbox.cr", "goonbox.to"},
	"filester": {"filester.sh", "filester.me"},
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
	return "", fmt.Errorf("could not auto-detect a site for %q (available: youtube, cyberdrop, bunker, goonbox, filester)", rawURL)
}

// runWithScraper launches the TUI immediately. The scraper runs in the
// background; the TUI shows a loading spinner until results arrive.
func runWithScraper(scraper model.Scraper, targetURL string, siteName string, opts globalOptions) error {
	cfg := loadGlobalConfig(opts)

	dl := downloader.NewDownloaderWithConfig(
		cfg.ConcurrentDownloads,
		cfg.Timeout,
		cfg.UserAgent,
	)

	// The scraper function runs in a background goroutine.
	scraperFn := func() ([]model.MediaItem, error) {
		return scraper.Parse(targetURL)
	}

	d := dashboard.New(scraperFn, dl, cfg.OutputDir, siteName)

	// Pass the URL slug so the dashboard can create a subfolder for albums
	d.SetAlbumSlug(extractSlug(targetURL))

	p := tea.NewProgram(d, tea.WithAltScreen())
	d.SetSendFunc(func(msg tea.Msg) { p.Send(msg) })

	// YouTube: wire up the quality-select downloader.
	if siteName == "youtube" {
		type ytDownloader interface {
			DownloadVideoWithQuality(videoID, dest string, height int) error
		}
		if yd, ok := scraper.(ytDownloader); ok {
			videoID := ""
			if yt, ok2 := scraper.(interface{ ExtractVideoID(string) string }); ok2 {
				videoID = yt.ExtractVideoID(targetURL)
			}
			d.SetYouTubeDownloader(yd, videoID)
		}
	}

	_, err := p.Run()
	return err
}

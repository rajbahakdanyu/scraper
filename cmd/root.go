package cmd

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/config"
	"github.com/user/scraper/internal/downloader"
	"github.com/user/scraper/internal/model"
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
		panic(err)
	}
}

func NewRootCmd() *cobra.Command {
	opts := globalOptions{}

	root := &cobra.Command{
		Use:   "scraper",
		Short: "Media scraper CLI tool",
		Long:  `Download media from various sites with a beautiful terminal UI.`,
	}

	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "Config file path (default: ~/.scraper.yaml)")
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", ".", "Output directory")
	root.PersistentFlags().IntVar(&opts.concurrency, "concurrent", 5, "Maximum concurrent downloads")

	root.AddCommand(
		newYouTubeCmd(opts),
		newCyberDropCmd(opts),
		newBunkerCmd(opts),
		newGoonBoxCmd(opts),
		newTUICmd(opts),
	)

	return root
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

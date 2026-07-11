package cmd

import (
	"fmt"

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
		newGoFileCmd(opts),
		newCyberDropCmd(opts),
		newBunkerCmd(opts),
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

// runWithScraper is the shared entry point for all site subcommands.
// It parses the URL and launches the TUI. For YouTube, the TUI shows a
// quality selector; for other sites, downloads auto-start.
func runWithScraper(scraper model.Scraper, targetURL string, siteName string, opts globalOptions) error {
	cfg := loadGlobalConfig(opts)

	items, err := scraper.Parse(targetURL)
	if err != nil && len(items) == 0 {
		return fmt.Errorf("%s: %w", scraper.Name(), err)
	}

	if len(items) == 0 {
		fmt.Println("No media items found.")
		return nil
	}

	dl := downloader.NewDownloaderWithConfig(
		cfg.ConcurrentDownloads,
		cfg.Timeout,
		cfg.UserAgent,
	)
	d := dashboard.New(items, dl, cfg.OutputDir, siteName)

	p := tea.NewProgram(d, tea.WithAltScreen())
	d.SetSendFunc(func(msg tea.Msg) { p.Send(msg) })

	// YouTube: show quality selector, let user pick one, then download
	// via DownloadVideoWithQuality (handles video+audio muxing).
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

	_, err = p.Run()
	return err
}

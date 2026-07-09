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
	noTUI       bool
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
	root.PersistentFlags().BoolVar(&opts.noTUI, "no-tui", false, "Disable TUI, use plain output")

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

// runWithScrrape is the shared entry point for all site subcommands.
// It parses the URL, then either launches the TUI (default) or falls
// back to plain CLI output (--no-tui).
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

	if opts.noTUI {
		return runScrape(scraper, items, targetURL, cfg.OutputDir, cfg)
	}

	return launchTUI(items, scraper, cfg, siteName)
}

// launchTUI starts the interactive dashboard for the given items.
func launchTUI(items []model.MediaItem, scraper model.Scraper, cfg *config.Config, site string) error {
	dl := downloader.NewDownloaderWithConfig(
		cfg.ConcurrentDownloads,
		cfg.Timeout,
		cfg.UserAgent,
	)
	d := dashboard.New(items, dl, cfg.OutputDir, site)

	// Start downloads automatically for YouTube (single item, no selection needed)
	if site == "youtube" {
		d.AutoStart()
	}

	p := tea.NewProgram(d, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

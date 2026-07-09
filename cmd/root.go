package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/config"
)

// globalOptions holds flags defined on the root command that are inherited
// by all subcommands via PersistentFlags.
type globalOptions struct {
	configPath  string
	output      string
	concurrency int
	uiMode      string
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
	root.PersistentFlags().StringVar(&opts.uiMode, "ui-mode", "minimal", "UI mode: full, minimal, quiet")

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

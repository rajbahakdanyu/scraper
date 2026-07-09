package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/downloader"
	"github.com/user/scraper/internal/model"
	"github.com/user/scraper/internal/parser/bunker"
	"github.com/user/scraper/internal/parser/cyberdrop"
	"github.com/user/scraper/internal/parser/gofile"
	"github.com/user/scraper/internal/parser/youtube"
	"github.com/user/scraper/internal/ui/dashboard"
)

// scraperRegistry maps a site name to its scraper, used to look up the
// right parser for `scraper tui <site> <url>`.
func scraperRegistry() map[string]model.Scraper {
	return map[string]model.Scraper{
		"youtube":   youtube.NewYouTubeScraper(),
		"gofile":    gofile.NewGoFileScraper(),
		"cyberdrop": cyberdrop.NewCyberDropScraper(),
		"bunker":    bunker.NewBunkerScraper(),
	}
}

func newTUICmd() *cobra.Command {
	var output string
	var concurrency int

	cmd := &cobra.Command{
		Use:   "tui <site> <url>",
		Short: "Launch the interactive terminal UI for a site",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			site, targetURL := args[0], args[1]

			registry := scraperRegistry()
			scraper, ok := registry[site]
			if !ok {
				return fmt.Errorf("unknown site %q (available: youtube, gofile, cyberdrop, bunker)", site)
			}

			items, err := scraper.Parse(targetURL)
			if err != nil {
				return fmt.Errorf("%s: %w", scraper.Name(), err)
			}

			dl := downloader.NewDownloader(concurrency)
			d := dashboard.New(items, dl, output)

			p := tea.NewProgram(d, tea.WithAltScreen())
			_, err = p.Run()
			return err
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", ".", "Output directory")
	cmd.Flags().IntVar(&concurrency, "concurrent", 5, "Maximum concurrent downloads")

	return cmd
}

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

func scraperRegistry() map[string]model.Scraper {
	return map[string]model.Scraper{
		"youtube":   youtube.NewYouTubeScraper(),
		"gofile":    gofile.NewGoFileScraper(),
		"cyberdrop": cyberdrop.NewCyberDropScraper(),
		"bunker":    bunker.NewBunkerScraper(),
	}
}

func newTUICmd(opts globalOptions) *cobra.Command {
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

			cfg := loadGlobalConfig(opts)

			items, err := scraper.Parse(targetURL)
			if err != nil && len(items) == 0 {
				return fmt.Errorf("%s: %w", scraper.Name(), err)
			}

			dl := downloader.NewDownloaderWithConfig(
				cfg.ConcurrentDownloads,
				cfg.Timeout,
				cfg.UserAgent,
			)
			d := dashboard.New(items, dl, cfg.OutputDir, site)

			p := tea.NewProgram(d, tea.WithAltScreen())
			_, err = p.Run()
			return err
		},
	}

	return cmd
}

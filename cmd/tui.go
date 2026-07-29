package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/model"
	"github.com/user/scraper/internal/parser/bunker"
	"github.com/user/scraper/internal/parser/cyberdrop"
	"github.com/user/scraper/internal/parser/filester"
	"github.com/user/scraper/internal/parser/goonbox"
	"github.com/user/scraper/internal/parser/youtube"
)

func scraperRegistry() map[string]model.Scraper {
	return map[string]model.Scraper{
		"youtube":   youtube.NewYouTubeScraper(),
		"cyberdrop": cyberdrop.NewCyberDropScraper(),
		"bunker":    bunker.NewBunkerScraper(),
		"goonbox":   goonbox.NewGoonBoxScraper(),
		"filester":  filester.NewFilesterScraper(),
	}
}

func newTUICmd(opts globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui [site] <url>",
		Short: "Launch the interactive terminal UI (site is auto-detected from the URL if omitted)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			site, targetURL := "", args[0]
			if len(args) == 2 {
				site, targetURL = args[0], args[1]
			} else {
				detected, err := detectSite(targetURL)
				if err != nil {
					return err
				}
				site = detected
			}

			registry := scraperRegistry()
			scraper, ok := registry[site]
			if !ok {
				return fmt.Errorf("unknown site %q (available: youtube, cyberdrop, bunker, goonbox, filester)", site)
			}

			return runWithScraper(scraper, targetURL, site, opts)
		},
	}

	return cmd
}

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/bunker"
)

func newBunkerCmd(opts globalOptions) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "bunker <url>",
		Short: "Scrape and download Bunker content",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, "Bunker")
			if err != nil {
				return err
			}

			cfg := loadGlobalConfig(opts)
			scraper := bunker.NewBunkerScraper()
			return runScrape(scraper, targetURL, cfg.OutputDir, cfg)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "Bunker URL")

	return cmd
}

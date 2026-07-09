package cmd

import (
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/cyberdrop"
)

func newCyberDropCmd(opts globalOptions) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "cyberdrop <url>",
		Short: "Scrape and download a CyberDrop gallery",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, "CyberDrop")
			if err != nil {
				return err
			}

			cfg := loadGlobalConfig(opts)
			scraper := cyberdrop.NewCyberDropScraper()
			return runScrape(scraper, targetURL, cfg.OutputDir, cfg)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "CyberDrop URL")

	return cmd
}

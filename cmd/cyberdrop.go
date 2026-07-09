package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/cyberdrop"
)

func newCyberDropCmd() *cobra.Command {
	var urlFlag string
	var output string
	var concurrency int

	cmd := &cobra.Command{
		Use:   "cyberdrop <url>",
		Short: "Scrape and download a CyberDrop gallery",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL := urlFlag
			if len(args) > 0 {
				targetURL = args[0]
			}
			if targetURL == "" {
				return errors.New("a CyberDrop URL is required (positional argument or --url)")
			}

			scraper := cyberdrop.NewCyberDropScraper()
			return runScrape(scraper, targetURL, output, concurrency)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "CyberDrop URL")
	cmd.Flags().StringVarP(&output, "output", "o", ".", "Output directory")
	cmd.Flags().IntVar(&concurrency, "concurrent", 5, "Maximum concurrent downloads")

	return cmd
}

// cmd/youtube.go
package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/youtube"
)

func newYouTubeCmd() *cobra.Command {
	var urlFlag string
	var output string
	var concurrency int

	cmd := &cobra.Command{
		Use:   "youtube <url>",
		Short: "Scrape and download a YouTube video",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL := urlFlag
			if len(args) > 0 {
				targetURL = args[0]
			}
			if targetURL == "" {
				return errors.New("a YouTube URL is required (positional argument or --url)")
			}

			scraper := youtube.NewYouTubeScraper()
			return runScrape(scraper, targetURL, output, concurrency)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "YouTube URL")
	cmd.Flags().StringVarP(&output, "output", "o", ".", "Output directory")
	cmd.Flags().IntVar(&concurrency, "concurrent", 5, "Maximum concurrent downloads")

	return cmd
}

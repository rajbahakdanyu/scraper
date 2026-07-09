package cmd

import (
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/youtube"
)

func newYouTubeCmd(opts globalOptions) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "youtube <url>",
		Short: "Scrape and download a YouTube video",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, "YouTube")
			if err != nil {
				return err
			}

			cfg := loadGlobalConfig(opts)
			scraper := youtube.NewYouTubeScraper()
			return runScrape(scraper, targetURL, cfg.OutputDir, cfg)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "YouTube URL")

	return cmd
}

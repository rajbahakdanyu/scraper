package cmd

import (
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/youtube"
)

func newYouTubeCmd(opts globalOptions) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "youtube <url>",
		Short: "Scrape and download a YouTube video or playlist",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, "YouTube")
			if err != nil {
				return err
			}
			return runWithScraper(youtube.NewYouTubeScraper(), targetURL, "youtube", opts)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "YouTube video or playlist URL")

	return cmd
}

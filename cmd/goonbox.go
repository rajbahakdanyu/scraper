package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/parser/goonbox"
)

func newGoonBoxCmd() *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "goonbox <url>",
		Short: "Scrape and download a GoonBox album",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, "GoonBox")
			if err != nil {
				return err
			}
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			return runWithScraper(goonbox.NewGoonBoxScraper(), targetURL, "goonbox", opts)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "GoonBox URL")

	return cmd
}

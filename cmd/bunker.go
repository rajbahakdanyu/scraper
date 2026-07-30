package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/parser/bunker"
)

func newBunkerCmd() *cobra.Command {
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
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			return runWithScraper(bunker.NewBunkerScraper(), targetURL, "bunker", opts)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "Bunker URL")

	return cmd
}

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/parser/cyberdrop"
)

func newCyberDropCmd() *cobra.Command {
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
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			return runWithScraper(cyberdrop.NewCyberDropScraper(), targetURL, "cyberdrop", opts)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "CyberDrop URL")

	return cmd
}

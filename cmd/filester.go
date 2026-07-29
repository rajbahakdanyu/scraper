// cmd/filester.go
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/filester"
)

func newFilesterCmd(opts globalOptions) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "filester <url>",
		Short: "Scrape and download a Filester file or folder",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, "Filester")
			if err != nil {
				return err
			}
			return runWithScraper(filester.NewFilesterScraper(), targetURL, "filester", opts)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "Filester URL")

	return cmd
}

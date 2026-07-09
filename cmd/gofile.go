package cmd

import (
	"github.com/spf13/cobra"

	"github.com/user/scraper/internal/parser/gofile"
)

func newGoFileCmd(opts globalOptions) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "gofile <url>",
		Short: "Scrape and download GoFile content",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, "GoFile")
			if err != nil {
				return err
			}
			return runWithScraper(gofile.NewGoFileScraper(), targetURL, "gofile", opts)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", "GoFile URL")

	return cmd
}

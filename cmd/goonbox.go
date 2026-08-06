package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/goonbox"
)

func newGoonBoxCmd() *cobra.Command {
	return newSiteCmd("goonbox <url>", "Scrape and download a GoonBox album", "goonbox", func() model.Scraper {
		return goonbox.NewGoonBoxScraper()
	})
}

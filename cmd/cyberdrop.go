package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/cyberdrop"
)

func newCyberDropCmd() *cobra.Command {
	return newSiteCmd("cyberdrop <url>", "Scrape and download a CyberDrop gallery", "cyberdrop", func() model.Scraper {
		return cyberdrop.NewCyberDropScraper()
	})
}

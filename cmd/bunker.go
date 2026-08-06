package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/bunker"
)

func newBunkerCmd() *cobra.Command {
	return newSiteCmd("bunker <url>", "Scrape and download Bunker content", "bunker", func() model.Scraper {
		return bunker.NewBunkerScraper()
	})
}

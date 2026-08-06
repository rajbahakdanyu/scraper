package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/filester"
)

func newFilesterCmd() *cobra.Command {
	return newSiteCmd("filester <url>", "Scrape and download a Filester file or folder", "filester", func() model.Scraper {
		return filester.NewFilesterScraper()
	})
}

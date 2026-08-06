package cmd

import (
	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
)

func newYouTubeCmd() *cobra.Command {
	return newSiteCmd("youtube <url>", "Scrape and download a YouTube video or playlist", "youtube", func() model.Scraper {
		return youtube.NewYouTubeScraper()
	})
}

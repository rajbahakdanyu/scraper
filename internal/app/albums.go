package app

import (
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

// BuildAlbums maps batch entries to filtered albums below the output root.
// A non-nil ytScraper marks a YouTube group and is shared by every entry;
// otherwise each entry uses its site's scraper from reg.
func BuildAlbums(cfg *config.Config, reg map[string]model.Scraper, entries []Entry, ytScraper model.Scraper, hooks Hooks) ([]model.Album, error) {
	albums := make([]model.Album, 0, len(entries))
	for _, e := range entries {
		site, scraper := "youtube", ytScraper
		slug := AlbumSlug(site, e.URL)
		if ytScraper == nil {
			site, _ = DetectSite(e.URL)
			scraper = reg[site]
			slug = DestSlug(site, e)
		}
		dest, err := fileutil.SafeJoin(cfg.OutputDir, slug)
		if err != nil {
			return nil, err
		}
		albums = append(albums, model.Album{
			Site:    site,
			URL:     e.URL,
			Slug:    slug,
			Dest:    dest,
			Scraper: NewFilterScraper(scraper, cfg, cfg.ScrapeDelay, cfg.DiskCheck, hooks),
		})
	}
	return albums, nil
}

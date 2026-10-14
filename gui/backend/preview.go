package backend

import (
	"context"
	"errors"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

// PreviewItem is one item a run would download.
type PreviewItem struct {
	Title string `json:"title"`
	Size  int64  `json:"size"`
	URL   string `json:"url"`
}

// PreviewAlbum is the dry-run listing of one input URL.
type PreviewAlbum struct {
	URL         string        `json:"url"`
	Site        string        `json:"site"`
	Slug        string        `json:"slug"`
	Items       []PreviewItem `json:"items"`
	TotalSize   int64         `json:"totalSize"`
	Unknown     int           `json:"unknown"`
	Warning     string        `json:"warning"`
	DiskWarning string        `json:"diskWarning"`
	Error       string        `json:"error"`
}

// Preview lists what the input would download, with sizes and a disk
// check, without downloading anything (--dry-run).
func (a *App) Preview(in RunInput) ([]PreviewAlbum, error) {
	p, err := parseInput(in)
	if err != nil {
		return nil, err
	}
	cfg, err := a.runConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.mu.Lock()
	if a.preview != nil {
		a.preview()
	}
	a.preview = cancel
	a.mu.Unlock()

	hooks := app.Hooks{Notice: a.notice}
	var reg map[string]model.Scraper
	if !p.single {
		if reg, err = a.registry(cfg.Proxy, cfg.ParsedCookies()); err != nil {
			return nil, err
		}
	}
	albums := make([]PreviewAlbum, 0, len(p.entries))
	for _, e := range p.entries {
		album := PreviewAlbum{URL: e.URL, Items: []PreviewItem{}}
		var scraper model.Scraper
		if p.single {
			album.Site, album.Slug = p.site, app.AlbumSlug(p.site, e.URL)
			scraper, err = a.newScraper(p.site, cfg.Proxy, cfg.ParsedCookies())
		} else {
			album.Site, err = app.DetectSite(e.URL)
			album.Slug = app.DestSlug(album.Site, e)
			scraper = reg[album.Site]
		}
		if err == nil {
			var pv app.Preview
			pv, err = app.PreviewAlbum(ctx, app.PreviewFilters(scraper, cfg, hooks), e.URL, cfg.OutputDir, cfg.DiskCheck)
			fillPreview(&album, pv)
		}
		if ctx.Err() != nil {
			return albums, errors.New("preview cancelled")
		}
		if err != nil {
			album.Error = err.Error()
		}
		albums = append(albums, album)
	}
	return albums, nil
}

func fillPreview(album *PreviewAlbum, pv app.Preview) {
	for _, it := range pv.Items {
		album.Items = append(album.Items, PreviewItem{Title: model.TitleOf(it), Size: it.Size, URL: it.URL})
	}
	album.TotalSize, album.Unknown = pv.TotalSize, pv.Unknown
	album.DiskWarning = pv.DiskWarning
	if pv.Warning != nil {
		album.Warning = pv.Warning.Error()
	}
}

// CancelPreview aborts a running Preview.
func (a *App) CancelPreview() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preview != nil {
		a.preview()
		a.preview = nil
	}
}

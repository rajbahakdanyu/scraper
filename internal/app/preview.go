package app

import (
	"context"
	"errors"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// Preview is a dry-run listing of one album.
type Preview struct {
	Items     []model.MediaItem
	TotalSize int64
	// Unknown counts items without a known size.
	Unknown int
	// NoItems reports a scrape that found nothing (ErrNoItems), which a
	// preview treats as informational rather than a failure.
	NoItems bool
	// Warning carries a partial-scrape error when some items still parsed.
	Warning     error
	DiskWarning string
}

// PreviewAlbum scrapes url and summarizes what a real run would download.
// A scrape that fails outright returns the error.
func PreviewAlbum(ctx context.Context, scraper model.Scraper, url, outputDir string, diskCheck bool) (Preview, error) {
	items, err := scraper.Parse(ctx, url)
	if err != nil && len(items) == 0 {
		if errors.Is(err, ErrNoItems) {
			return Preview{NoItems: true}, nil
		}
		return Preview{}, err
	}
	p := Preview{Items: items, Warning: err}
	for _, item := range items {
		if item.Size > 0 {
			p.TotalSize += item.Size
		} else {
			p.Unknown++
		}
	}
	if diskCheck {
		p.DiskWarning = DiskCheck(items, outputDir)
	}
	return p, nil
}

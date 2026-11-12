package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/disk"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

// ErrNoItems marks a scrape that returned zero items (a dead URL, a DOM
// change) as distinct from "everything was filtered out": the first is a
// failure and must exit non-zero, the second is the user's filter doing
// its job.
var ErrNoItems = errors.New("no media items found")

// Hooks receives in-run warnings and run-log lines. Nil funcs are no-ops.
type Hooks struct {
	Notice func(msg string)
	Log    func(msg string)
}

func (h Hooks) notice(format string, args ...any) {
	if h.Notice != nil {
		h.Notice(fmt.Sprintf(format, args...))
	}
}

func (h Hooks) log(format string, args ...any) {
	if h.Log != nil {
		h.Log(fmt.Sprintf(format, args...))
	}
}

// FilterScraper wraps a scraper with every configured per-album filter: the
// item cap, the media allowlist, include/exclude globs, size bounds, and an
// optional one-time disk-space check.
type FilterScraper struct {
	inner            model.Scraper
	include, exclude []string
	minSize, maxSize int64
	mediaOnly        bool
	mediaExtensions  []string
	maxItems         int
	scrapeDelay      time.Duration
	diskCheck        bool
	outputDir        string
	diskChecked      bool
	hooks            Hooks
}

// NewFilterScraper wraps inner with cfg's filters. cfg must be finalized.
func NewFilterScraper(inner model.Scraper, cfg *config.Config, scrapeDelay time.Duration, diskCheck bool, hooks Hooks) *FilterScraper {
	if configurable, ok := inner.(interface{ SetDownloadOptions(string, string) }); ok {
		configurable.SetDownloadOptions(cfg.Quality, cfg.AudioOnly)
	}
	return &FilterScraper{
		inner:           inner,
		include:         cfg.Include,
		exclude:         cfg.Exclude,
		minSize:         cfg.MinSizeBytes(),
		maxSize:         cfg.MaxSizeBytes(),
		mediaOnly:       cfg.MediaOnly,
		mediaExtensions: cfg.MediaExtensions,
		maxItems:        cfg.MaxAlbumItems,
		scrapeDelay:     scrapeDelay,
		diskCheck:       diskCheck,
		outputDir:       cfg.OutputDir,
		hooks:           hooks,
	}
}

// PreviewFilters wraps a scraper with the filters for preview (dry-run)
// paths, so previewed counts match what a real run downloads. The disk
// check stays with Preview itself to avoid reporting it twice.
func PreviewFilters(scraper model.Scraper, cfg *config.Config, hooks Hooks) model.Scraper {
	return NewFilterScraper(scraper, cfg, cfg.ScrapeDelay, false, hooks)
}

// ScrapeDelaySleeper is the test seam for the configured inter-scrape delay.
var ScrapeDelaySleeper netutil.Sleeper = netutil.RealSleeper{}

func (f *FilterScraper) Name() string { return f.inner.Name() }

func (f *FilterScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	if f.scrapeDelay > 0 {
		if err := ScrapeDelaySleeper.SleepCtx(ctx, f.scrapeDelay); err != nil {
			return nil, err
		}
	}
	items, err := f.inner.Parse(ctx, url)
	// A partial scrape (some pages failed) still carries usable items;
	// dropping them here would turn a warning into an empty album. Only a
	// total failure aborts.
	if err != nil {
		f.hooks.log("scrape error: %s: %v", url, err)
	}
	if err != nil && len(items) == 0 {
		return nil, err
	}
	if len(items) == 0 && err == nil {
		// The scrape itself found nothing (a dead URL or a changed
		// page): the batch must report it, not print a quiet warning.
		return nil, ErrNoItems
	}
	items = ApplyAlbumItemCap(items, f.maxItems, f.hooks)
	if f.mediaOnly && f.inner.Name() != "youtube" {
		items = ApplyMediaFilter(items, f.mediaExtensions, f.hooks)
	}
	filtered := items
	if len(f.include) > 0 || len(f.exclude) > 0 || f.minSize > 0 || f.maxSize > 0 {
		var summary model.FilterSummary
		filtered, summary = model.FilterItems(items, f.include, f.exclude, f.minSize, f.maxSize)
		f.hooks.notice("filters: %s", summary)
		if len(filtered) == 0 {
			f.hooks.notice("No items matched filters.")
		}
	}
	if f.diskCheck && !f.diskChecked {
		f.diskChecked = true
		if msg := DiskCheck(filtered, f.outputDir); msg != "" {
			f.hooks.notice("%s", msg)
		}
	}
	return filtered, err
}

// ApplyAlbumItemCap truncates an album's item list to the configured
// maximum, warning when it does: a hostile or broken album page can list
// thousands of entries and turn one URL into an endless run. Zero or
// negative means unlimited.
func ApplyAlbumItemCap(items []model.MediaItem, max int, hooks Hooks) []model.MediaItem {
	if max <= 0 || len(items) <= max {
		return items
	}
	hooks.notice("album item cap: keeping first %d of %d items (max_album_items)", max, len(items))
	return items[:max]
}

// ApplyMediaFilter drops non-media items and reports the skip count, so the
// single, batch, and preview paths all behave the same. The message is only
// emitted when something was actually skipped; an all-skipped album says so.
func ApplyMediaFilter(items []model.MediaItem, extraExtensions []string, hooks Hooks) []model.MediaItem {
	kept, skipped := model.NewMediaFilter(extraExtensions).Filter(items)
	if skipped > 0 {
		hooks.notice("media filter: %d/%d items (%d skipped non-media)", len(kept), len(items), skipped)
	}
	if len(items) > 0 && len(kept) == 0 {
		hooks.notice("No media items found.")
	}
	return kept
}

// DiskCheck returns a warning when the known item sizes exceed the free
// space at outputDir, or when free space cannot be determined; "" otherwise.
func DiskCheck(items []model.MediaItem, outputDir string) string {
	var total int64
	unknown := 0
	for _, it := range items {
		if it.Size > 0 {
			total += it.Size
		} else {
			unknown++
		}
	}
	if total == 0 {
		return ""
	}
	free, err := disk.FreeSpaceNearestExisting(outputDir)
	if err != nil {
		return fmt.Sprintf("disk: could not check free space for %s: %v", outputDir, err)
	}
	if free < total {
		return DiskWarning(total, free, unknown)
	}
	return ""
}

// DiskWarning formats the low-disk-space warning.
func DiskWarning(total, free int64, unknown int) string {
	msg := fmt.Sprintf("disk: %s free, %s needed (%s short)",
		disk.FormatBytes(free), disk.FormatBytes(total), disk.FormatBytes(total-free))
	if unknown > 0 {
		msg += fmt.Sprintf(" (%d items with unknown size)", unknown)
	}
	return msg
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/disk"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

func runDryRunWithDiskCheck(scraper model.Scraper, targetURL, outputDir string, diskCheck bool) error {
	fmt.Printf("Scraping %s...\n", targetURL)

	items, err := scraper.Parse(context.Background(), targetURL)
	if err != nil && len(items) == 0 {
		if errors.Is(err, ErrNoItems) {
			// Dry-run is informational: a URL that yields nothing
			// prints the empty message and exits 0.
			fmt.Println("No items found.")
			return nil
		}
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	if len(items) == 0 {
		fmt.Println("No items found.")
		return nil
	}
	runDiskCheck(items, outputDir, diskCheck)

	var totalSize int64
	var unknownSize int
	for _, item := range items {
		title := model.TitleOf(item)
		sizeStr := "unknown size"
		if item.Size > 0 {
			totalSize += item.Size
			sizeStr = dashboard.HumanizeBytes(item.Size)
		} else {
			unknownSize++
		}
		fmt.Printf("  %-60s %s\n", title, sizeStr)
	}

	fmt.Println()
	if unknownSize > 0 {
		fmt.Printf("%d items, %s total (%d with unknown size)\n", len(items), dashboard.HumanizeBytes(totalSize), unknownSize)
	} else {
		fmt.Printf("%d items, %s total\n", len(items), dashboard.HumanizeBytes(totalSize))
	}
	return nil
}

type filterScraper struct {
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
}

// newFilterScraper wraps inner with every configured per-album filter: the
// item cap, the media allowlist, include/exclude globs, size bounds, and an
// optional one-time disk-space check.
func newFilterScraper(inner model.Scraper, cfg *config.Config, scrapeDelay time.Duration, diskCheck bool) *filterScraper {
	return &filterScraper{
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
	}
}

// scrapeDelaySleeper is the test seam for the configured inter-scrape delay.
var scrapeDelaySleeper netutil.Sleeper = netutil.RealSleeper{}

func (f *filterScraper) Name() string { return f.inner.Name() }

func (f *filterScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	if f.scrapeDelay > 0 {
		if err := scrapeDelaySleeper.SleepCtx(ctx, f.scrapeDelay); err != nil {
			return nil, err
		}
	}
	items, err := f.inner.Parse(ctx, url)
	// A partial scrape (some pages failed) still carries usable items;
	// dropping them here would turn a warning into an empty album. Only a
	// total failure aborts.
	if err != nil {
		logLine("scrape error: %s: %v", url, err)
	}
	if err != nil && len(items) == 0 {
		return nil, err
	}
	if len(items) == 0 && err == nil {
		// The scrape itself found nothing (a dead URL or a changed
		// page): the batch must report it, not print a quiet warning.
		return nil, ErrNoItems
	}
	items = applyAlbumItemCap(items, f.maxItems)
	if f.mediaOnly && f.inner.Name() != "youtube" {
		items = applyMediaFilter(items, f.mediaExtensions)
	}
	filtered := items
	if len(f.include) > 0 || len(f.exclude) > 0 || f.minSize > 0 || f.maxSize > 0 {
		var summary model.FilterSummary
		filtered, summary = model.FilterItems(items, f.include, f.exclude, f.minSize, f.maxSize)
		notice("filters: %s\n", summary)
		if len(filtered) == 0 {
			notice("No items matched filters.\n")
		}
	}
	if f.diskCheck && !f.diskChecked {
		f.diskChecked = true
		runDiskCheck(filtered, f.outputDir, true)
	}
	return filtered, err
}

// printItemURLs writes one item URL per line to w, skipping items without
// a URL (nothing to fetch).
func printItemURLs(w io.Writer, items []model.MediaItem) {
	for _, item := range items {
		if item.URL == "" {
			continue
		}
		fmt.Fprintln(w, item.URL)
	}
}

// applyAlbumItemCap truncates an album's item list to the configured
// maximum, warning when it does: a hostile or broken album page can list
// thousands of entries and turn one URL into an endless run. Zero or
// negative means unlimited.
func applyAlbumItemCap(items []model.MediaItem, max int) []model.MediaItem {
	if max <= 0 || len(items) <= max {
		return items
	}
	notice("album item cap: keeping first %d of %d items (max_album_items)\n", max, len(items))
	return items[:max]
}

// applyMediaFilter drops non-media items and reports the skip count, so the
// single, batch, and dry-run paths all behave the same. The message is only
// printed when something was actually skipped; an all-skipped album says so.
func applyMediaFilter(items []model.MediaItem, extraExtensions []string) []model.MediaItem {
	kept, skipped := model.NewMediaFilter(extraExtensions).Filter(items)
	if skipped > 0 {
		notice("media filter: %d/%d items (%d skipped non-media)\n", len(kept), len(items), skipped)
	}
	if len(items) > 0 && len(kept) == 0 {
		notice("No media items found.\n")
	}
	return kept
}

func runDiskCheck(items []model.MediaItem, outputDir string, diskCheck bool) {
	if !diskCheck {
		return
	}
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
		return
	}
	free, err := disk.FreeSpaceNearestExisting(outputDir)
	if err != nil {
		// The check was requested: failing to see the filesystem is
		// worth a line, not silence.
		notice("disk: could not check free space for %s: %v\n", outputDir, err)
		return
	}
	if free < total {
		notice("%s\n", diskWarning(total, free, unknown))
	}
}

func diskWarning(total, free int64, unknown int) string {
	msg := fmt.Sprintf("disk: %s free, %s needed (%s short)",
		disk.FormatBytes(free), disk.FormatBytes(total), disk.FormatBytes(total-free))
	if unknown > 0 {
		msg += fmt.Sprintf(" (%d items with unknown size)", unknown)
	}
	return msg
}

// applyDryRunFilters wraps a scraper with the include/exclude/size filters
// for dry-run paths, so previewed counts match what a real run downloads.
// Disk check stays with the dry-run functions themselves to avoid printing
// the warning twice.
func applyDryRunFilters(scraper model.Scraper, cfg *config.Config) model.Scraper {
	return newFilterScraper(scraper, cfg, cfg.ScrapeDelay, false)
}

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
	"golang.org/x/term"
)

// helpRequested reports whether the root command should print help instead
// of running: no URLs can come from anywhere (no positional args, no
// --input-file, and stdin is a terminal). Piped stdin or an input file must
// proceed so collectBatchURLs can read them.
func helpRequested(args []string, inputFileSet, stdinIsTTY bool) bool {
	return len(args) == 0 && !inputFileSet && stdinIsTTY
}

// stdinIsTerminal is a test seam for the bare-invocation help check.
var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// batchEntry is one input line: a URL to scrape and, optionally, the album
// URL that item belongs to. Paired entries (retry lists) let the download
// land in its original album folder while scraping only the item.
type batchEntry struct {
	URL      string
	AlbumURL string
}

// collectBatchURLs gathers entries from positional arguments, --input-file,
// and stdin (when not a TTY and no other sources). Lines starting with "#"
// are treated as comments and blank lines are skipped.
func collectBatchURLs(args []string, opts globalOptions) ([]batchEntry, error) {
	var entries []batchEntry
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			entries = append(entries, batchEntry{URL: arg})
		}
	}

	if opts.inputFileSet {
		fileEntries, err := readURLsFromFile(opts.inputFile)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}

	if len(entries) == 0 && !term.IsTerminal(int(os.Stdin.Fd())) {
		stdinEntries, err := readURLsFromReader(os.Stdin)
		if err != nil {
			return nil, err
		}
		entries = append(entries, stdinEntries...)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no URLs provided (use positional args, --input-file, or stdin)")
	}
	return entries, nil
}

func readURLsFromFile(path string) ([]batchEntry, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open input file %q: %w", path, err)
	}
	defer f.Close()
	return readURLsFromReader(f)
}

// readURLsFromReader parses one entry per non-comment line. A line may be a
// single URL, or an item URL and its album URL separated by a tab (the
// retry-list format); anything after a second tab is ignored.
func readURLsFromReader(r io.Reader) ([]batchEntry, error) {
	var entries []batchEntry
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		entry := batchEntry{URL: strings.TrimSpace(fields[0])}
		if len(fields) > 1 {
			entry.AlbumURL = strings.TrimSpace(fields[1])
		}
		if entry.URL == "" {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read URLs: %w", err)
	}
	return entries, nil
}

// batchDestSlug picks the destination folder for a batch entry: the album's
// slug when the entry carries an album URL (a retry pair), else the entry's
// own slug.
func batchDestSlug(siteName string, e batchEntry) string {
	if e.AlbumURL != "" {
		return albumSlugFor(siteName, e.AlbumURL)
	}
	return albumSlugFor(siteName, e.URL)
}

// runBatchWithConfig orchestrates scraping multiple album URLs with a shared
// download queue. It scrapes albums sequentially — feeding items to the
// downloader as soon as each album finishes — so token-based URLs from early
// albums don't expire before later albums are scraped.
func runBatchWithConfig(cfg *config.Config, opts globalOptions, entries []batchEntry) error {
	reg, err := batchScraperRegistry(cfg.Proxy, cfg.ParsedCookies())
	if err != nil {
		return err
	}

	if opts.printURLs {
		if opts.json {
			return fmt.Errorf("--print-urls and --json are mutually exclusive")
		}
		var errs []error
		for _, e := range entries {
			site, siteErr := detectSite(e.URL)
			if siteErr != nil {
				fmt.Fprintf(os.Stderr, "print-urls: %s: %v\n", e.URL, siteErr)
				errs = append(errs, siteErr)
				continue
			}
			scraper := applyDryRunFilters(reg[site], cfg)
			items, parseErr := scraper.Parse(context.Background(), e.URL)
			if parseErr != nil && len(items) == 0 {
				fmt.Fprintf(os.Stderr, "print-urls: %s: %v\n", e.URL, parseErr)
				errs = append(errs, parseErr)
				continue
			}
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", parseErr)
			}
			printItemURLs(os.Stdout, items)
		}
		return model.NewPartialError("print-urls", errs)
	}

	if opts.dryRun {
		if opts.json {
			var allErrs []error
			for _, e := range entries {
				site, sitErr := detectSite(e.URL)
				if sitErr != nil {
					allErrs = append(allErrs, sitErr)
					continue
				}
				if jsonErr := runDryRunJSONWithDiskCheck(applyDryRunFilters(reg[site], cfg), e.URL, site, batchDestSlug(site, e), os.Stdout, cfg.OutputDir, cfg.DiskCheck); jsonErr != nil {
					fmt.Fprintf(os.Stderr, "dry-run: %s: %v\n", e.URL, jsonErr)
					allErrs = append(allErrs, jsonErr)
				}
			}
			return model.NewPartialError("batch dry-run", allErrs)
		}
		var errs []error
		for _, e := range entries {
			site, sitErr := detectSite(e.URL)
			if sitErr != nil {
				errs = append(errs, sitErr)
				continue
			}
			if dryErr := runDryRunWithDiskCheck(applyDryRunFilters(reg[site], cfg), e.URL, cfg.OutputDir, cfg.DiskCheck); dryErr != nil {
				fmt.Fprintf(os.Stderr, "dry-run: %s: %v\n", e.URL, dryErr)
				errs = append(errs, dryErr)
			}
		}
		return model.NewPartialError("batch dry-run", errs)
	}

	var youtubeEntries, genericEntries []batchEntry
	for _, e := range entries {
		site, err := detectSite(e.URL)
		if err != nil {
			return err
		}
		if site == "youtube" {
			youtubeEntries = append(youtubeEntries, e)
		} else {
			genericEntries = append(genericEntries, e)
		}
	}

	rateLimitBytesPerSec, err := config.ParseRateLimit(cfg.RateLimit)
	if err != nil {
		return err
	}

	// Mixed lists run as sequential same-type groups, each with its own
	// downloader: the dashboard expects a single downloader per run. A
	// failed group never stops the next one — one dead YouTube video
	// must not silently skip every other URL in the list.
	var groupErrs []error
	if len(youtubeEntries) > 0 {
		if err := runBatchGroup(cfg, opts, reg, youtubeEntries, true, rateLimitBytesPerSec); err != nil {
			groupErrs = append(groupErrs, err)
		}
	}
	if len(genericEntries) > 0 {
		if err := runBatchGroup(cfg, opts, reg, genericEntries, false, rateLimitBytesPerSec); err != nil {
			groupErrs = append(groupErrs, err)
		}
	}
	return model.NewPartialError("batch", groupErrs)
}

// runBatchGroup runs one same-type group of batch entries with its own
// downloader and dashboard run.
func runBatchGroup(cfg *config.Config, opts globalOptions, reg map[string]model.Scraper, entries []batchEntry, youtube bool, rateLimitBytesPerSec int64) error {
	var dl model.Downloader
	// Build dashAlbumURL entries for the dashboard: one scraper per URL so
	// the incremental scrape chain can call the right parser.
	dashAlbums := make([]model.Album, 0, len(entries))
	if youtube {
		scraper := batchYouTubeScraper()
		ytDl, err := batchYouTubeDownloader(scraper, cfg, rateLimitBytesPerSec)
		if err != nil {
			return fmt.Errorf("youtube downloader: %w", err)
		}
		dl = ytDl
		defer dl.Shutdown()
		for _, e := range entries {
			slug := albumSlugFor("youtube", e.URL)
			dest, err := fileutil.SafeJoin(cfg.OutputDir, slug)
			if err != nil {
				return err
			}
			dashAlbums = append(dashAlbums, model.Album{
				Site:    "youtube",
				URL:     e.URL,
				Slug:    slug,
				Dest:    dest,
				Scraper: scraper,
			})
		}
	} else {
		genericDl, err := batchGenericDownloader(cfg, rateLimitBytesPerSec)
		if err != nil {
			return fmt.Errorf("downloader: %w", err)
		}
		dl = genericDl
		defer dl.Shutdown()
		for _, e := range entries {
			site, _ := detectSite(e.URL)
			slug := batchDestSlug(site, e)
			dest, err := fileutil.SafeJoin(cfg.OutputDir, slug)
			if err != nil {
				return err
			}
			scraper := reg[site]
			dashAlbums = append(dashAlbums, model.Album{
				Site:    site,
				URL:     e.URL,
				Slug:    slug,
				Dest:    dest,
				Scraper: scraper,
			})
		}
	}

	for i := range dashAlbums {
		dashAlbums[i].Scraper = newFilterScraper(dashAlbums[i].Scraper, cfg, cfg.ScrapeDelay, cfg.DiskCheck)
	}

	return finishRun(cfg, opts,
		func() error {
			return runBatchJSON(dl, dashAlbums, cfg.OutputDir, cfg.Quality, cfg.AudioOnly, os.Stdout)
		},
		func() error {
			return runBatchPlain(dl, dashAlbums, cfg.OutputDir, os.Stdout)
		},
		func() error {
			return runTUI(dashboard.NewBatch(dl, cfg.OutputDir, dashAlbums), cfg.OutputDir)
		},
	)
}

var batchScraperRegistry = scraperRegistry

var batchYouTubeScraper = func() model.Scraper {
	return youtube.NewYouTubeScraper()
}

var batchYouTubeDownloader = func(scraper model.Scraper, cfg *config.Config, rateLimitBytesPerSec int64) (model.Downloader, error) {
	return buildDownloader(scraper, "youtube", cfg, rateLimitBytesPerSec)
}

var batchGenericDownloader = func(cfg *config.Config, rateLimitBytesPerSec int64) (model.Downloader, error) {
	return buildDownloader(nil, "generic", cfg, rateLimitBytesPerSec)
}

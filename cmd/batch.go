package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
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

// collectBatchURLs gathers entries from positional arguments, --input-file,
// and stdin (when not a TTY and no other sources). Lines starting with "#"
// are treated as comments and blank lines are skipped.
func collectBatchURLs(args []string, opts globalOptions) ([]app.Entry, error) {
	var entries []app.Entry
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			entries = append(entries, app.Entry{URL: arg})
		}
	}

	if opts.inputFileSet {
		fileEntries, err := app.ReadURLFile(opts.inputFile)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}

	if len(entries) == 0 && !term.IsTerminal(int(os.Stdin.Fd())) {
		stdinEntries, err := app.ParseURLList(os.Stdin)
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

// runBatchWithConfig orchestrates scraping multiple album URLs with a shared
// download queue. It scrapes albums sequentially — feeding items to the
// downloader as soon as each album finishes — so token-based URLs from early
// albums don't expire before later albums are scraped.
func runBatchWithConfig(cfg *config.Config, opts globalOptions, entries []app.Entry) error {
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
			site, siteErr := app.DetectSite(e.URL)
			if siteErr != nil {
				fmt.Fprintf(os.Stderr, "print-urls: %s: %v\n", e.URL, siteErr)
				errs = append(errs, siteErr)
				continue
			}
			scraper := app.PreviewFilters(reg[site], cfg, cliHooks)
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
				site, sitErr := app.DetectSite(e.URL)
				if sitErr != nil {
					allErrs = append(allErrs, sitErr)
					continue
				}
				if jsonErr := runDryRunJSONWithDiskCheck(app.PreviewFilters(reg[site], cfg, cliHooks), e.URL, site, app.DestSlug(site, e), os.Stdout, cfg.OutputDir, cfg.DiskCheck); jsonErr != nil {
					fmt.Fprintf(os.Stderr, "dry-run: %s: %v\n", e.URL, jsonErr)
					allErrs = append(allErrs, jsonErr)
				}
			}
			return model.NewPartialError("batch dry-run", allErrs)
		}
		var errs []error
		for _, e := range entries {
			site, sitErr := app.DetectSite(e.URL)
			if sitErr != nil {
				errs = append(errs, sitErr)
				continue
			}
			if dryErr := runDryRunWithDiskCheck(app.PreviewFilters(reg[site], cfg, cliHooks), e.URL, cfg.OutputDir, cfg.DiskCheck); dryErr != nil {
				fmt.Fprintf(os.Stderr, "dry-run: %s: %v\n", e.URL, dryErr)
				errs = append(errs, dryErr)
			}
		}
		return model.NewPartialError("batch dry-run", errs)
	}

	youtubeEntries, genericEntries, err := app.SplitGroups(entries)
	if err != nil {
		return err
	}
	if _, err := config.ParseRateLimit(cfg.RateLimit); err != nil {
		return err
	}

	// Mixed lists run as sequential same-type groups, each with its own
	// downloader: the dashboard expects a single downloader per run. A
	// failed group never stops the next one — one dead YouTube video
	// must not silently skip every other URL in the list.
	var groupErrs []error
	if len(youtubeEntries) > 0 {
		if err := runBatchGroup(cfg, opts, reg, youtubeEntries, true); err != nil {
			groupErrs = append(groupErrs, err)
		}
	}
	if len(genericEntries) > 0 {
		if err := runBatchGroup(cfg, opts, reg, genericEntries, false); err != nil {
			groupErrs = append(groupErrs, err)
		}
	}
	return model.NewPartialError("batch", groupErrs)
}

// runBatchGroup runs one same-type group of batch entries with its own
// downloader and dashboard run.
func runBatchGroup(cfg *config.Config, opts globalOptions, reg map[string]model.Scraper, entries []app.Entry, isYouTube bool) error {
	var dl model.Downloader
	var ytScraper model.Scraper
	if isYouTube {
		ytScraper = batchYouTubeScraper()
		ytDl, err := batchYouTubeDownloader(ytScraper, cfg)
		if err != nil {
			return fmt.Errorf("youtube downloader: %w", err)
		}
		dl = ytDl
	} else {
		genericDl, err := batchGenericDownloader(cfg)
		if err != nil {
			return fmt.Errorf("downloader: %w", err)
		}
		dl = genericDl
	}
	defer dl.Shutdown()

	albums, err := app.BuildAlbums(cfg, reg, entries, ytScraper, cliHooks)
	if err != nil {
		return err
	}

	return finishRun(cfg, opts,
		func() error {
			return runBatchJSON(dl, albums, cfg.OutputDir, cfg.Quality, cfg.AudioOnly, os.Stdout)
		},
		func() error {
			return runBatchPlain(dl, albums, cfg.OutputDir, os.Stdout)
		},
		func() error {
			return runTUI(dashboard.NewBatch(dl, cfg.OutputDir, albums), cfg.OutputDir)
		},
	)
}

var batchScraperRegistry = app.Registry

var batchYouTubeScraper = func() model.Scraper {
	return youtube.NewYouTubeScraper()
}

var batchYouTubeDownloader = func(scraper model.Scraper, cfg *config.Config) (model.Downloader, error) {
	return app.BuildDownloader(scraper, "youtube", cfg, cliHooks)
}

var batchGenericDownloader = func(cfg *config.Config) (model.Downloader, error) {
	return app.BuildDownloader(nil, "generic", cfg, cliHooks)
}

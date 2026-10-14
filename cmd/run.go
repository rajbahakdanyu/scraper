package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/downloader"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
	"golang.org/x/term"
)

// newRunOnce returns the root command's run step. The config (log file,
// browser cookies, ...) is loaded once and reused by every --watch
// iteration.
func newRunOnce(opts globalOptions, entries []batchEntry) func() error {
	var cfg *config.Config
	return func() error {
		if cfg == nil {
			loaded, err := configLoader(opts)
			if err != nil {
				return err
			}
			cfg = loaded
		}
		if len(entries) == 1 && !opts.inputFileSet {
			targetURL := entries[0].URL
			site, err := detectSite(targetURL)
			if err != nil {
				return err
			}
			return runSingleWithConfig(cfg, opts, site, targetURL)
		}
		return runBatchWithConfig(cfg, opts, entries)
	}
}

// runSingle runs one URL with the site's scraper built from the loaded
// config, so --proxy and cookies apply to scraping as well as downloading.
func runSingle(opts globalOptions, site, targetURL string) error {
	cfg, err := configLoader(opts)
	if err != nil {
		return err
	}
	return runSingleWithConfig(cfg, opts, site, targetURL)
}

func runSingleWithConfig(cfg *config.Config, opts globalOptions, site, targetURL string) error {
	scraper, err := newSiteScraper(site, cfg.Proxy, cfg.ParsedCookies())
	if err != nil {
		return err
	}
	return runWithScraperAndConfig(cfg, scraper, targetURL, site, opts)
}

// runWithScraperAndConfig launches the TUI immediately using an
// already-loaded config, avoiding a second config-file parse/cookie read on
// the root command path. The scraper runs in the background; the TUI shows
// a loading spinner until results arrive.
func runWithScraperAndConfig(cfg *config.Config, scraper model.Scraper, targetURL, siteName string, opts globalOptions) error {
	// The scraper function runs in a background goroutine.
	filtered := newFilterScraper(scraper, cfg, 0, cfg.DiskCheck)
	scraperFn := func(ctx context.Context) ([]model.MediaItem, error) {
		return filtered.Parse(ctx, targetURL)
	}

	if opts.dryRun {
		filtered := applyDryRunFilters(scraper, cfg)
		if opts.json {
			return runDryRunJSONWithDiskCheck(filtered, targetURL, siteName, "", os.Stdout, cfg.OutputDir, cfg.DiskCheck)
		}
		return runDryRunWithDiskCheck(filtered, targetURL, cfg.OutputDir, cfg.DiskCheck)
	}

	if opts.printURLs {
		if opts.json {
			return fmt.Errorf("--print-urls and --json are mutually exclusive")
		}
		items, err := scraperFn(context.Background())
		if err != nil && len(items) == 0 {
			return err
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		printItemURLs(os.Stdout, items)
		return nil
	}
	rateLimitBytesPerSec, err := config.ParseRateLimit(cfg.RateLimit)
	if err != nil {
		return err
	}

	dl, err := buildDownloader(scraper, siteName, cfg, rateLimitBytesPerSec)
	if err != nil {
		return err
	}
	defer dl.Shutdown()

	slug := albumSlugFor(siteName, targetURL)

	// When stdout is not a terminal (piped, redirected, cron), the
	// bubbletea TUI would emit escape-code garbage — fall back to plain
	// line-based output instead.
	return finishRun(cfg, opts,
		func() error {
			return runJSON(dl, func() ([]model.MediaItem, error) {
				return scraperFn(context.Background())
			}, cfg.OutputDir, slug, siteName, targetURL, cfg.Quality, cfg.AudioOnly, os.Stdout)
		},
		func() error {
			return runPlain(dl, func() ([]model.MediaItem, error) {
				return scraperFn(context.Background())
			}, cfg.OutputDir, slug, targetURL, os.Stdout)
		},
		func() error {
			d := dashboard.New(scraperFn, dl, cfg.OutputDir, siteName)
			d.SetAlbumSlug(slug)
			d.SetSourceURL(targetURL)
			return runTUI(d, cfg.OutputDir)
		},
	)
}

// runTUI runs the dashboard and, once it exits, writes the retry list and
// reports failures as an error so the exit code matches plain/JSON runs.
func runTUI(d *dashboard.Dashboard, outputDir string) error {
	d.SetRetryWriter(func(items []dashboard.FailedItem) (string, error) {
		path, _, err := retryReport(outputDir, failuresFromDashboard(items), time.Now())
		return path, err
	})
	final, err := tea.NewProgram(d, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		return err
	}
	return tuiResult(final, outputDir)
}

func tuiResult(final tea.Model, outputDir string) error {
	dash, ok := final.(*dashboard.Dashboard)
	if !ok {
		return nil
	}
	if _, summary, rErr := retryReport(outputDir, failuresFromDashboard(dash.Failures()), time.Now()); rErr != nil {
		fmt.Fprintf(os.Stderr, "retry list: %v\n", rErr)
	} else if summary != "" {
		fmt.Fprintln(os.Stdout, summary)
	}
	return dash.Result()
}

func buildDownloader(scraper model.Scraper, siteName string, cfg *config.Config, rateLimitBytesPerSec int64) (dashboard.Downloader, error) {
	if siteName == "youtube" {
		ytScraper, ok := scraper.(*youtube.Scraper)
		if !ok {
			return nil, fmt.Errorf("youtube: unexpected scraper type %T", scraper)
		}
		dl, err := youtube.NewDownloaderWithConfig(ytScraper, youtube.DownloaderConfig{
			Concurrency:  cfg.ConcurrentDownloads,
			Timeout:      cfg.Timeout,
			UserAgent:    cfg.UserAgent,
			RateLimit:    rateLimitBytesPerSec,
			SkipChecksum: !cfg.Checksums,
			ProxyURL:     cfg.Proxy,
			Cookies:      cfg.ParsedCookies(),
			Quality:      cfg.Quality,
			AudioOnly:    cfg.AudioOnly,
			Warn:         func(msg string) { notice("%s\n", msg) },
		})
		if err != nil {
			return nil, fmt.Errorf("youtube downloader: %w", err)
		}
		return dl, nil
	}

	dl, err := downloader.NewDownloaderWithConfig(
		cfg.ConcurrentDownloads,
		cfg.Timeout,
		cfg.UserAgent,
		cfg.Proxy,
		cfg.ParsedCookies(),
	)
	if err != nil {
		return nil, fmt.Errorf("downloader: %w", err)
	}
	dl.SetRateLimit(rateLimitBytesPerSec)
	dl.SetSkipChecksums(!cfg.Checksums)
	return dl, nil
}

func finishRun(cfg *config.Config, opts globalOptions, jsonRun, plainRun, tuiRun func() error) error {
	var err error
	if opts.json {
		err = jsonRun()
	} else if opts.noTui || !term.IsTerminal(int(os.Stdout.Fd())) {
		err = plainRun()
	} else {
		err = withBufferedNotices(tuiRun)
	}
	if cfg.Notify {
		tryNotify(notificationSummary(err))
	}
	return err
}

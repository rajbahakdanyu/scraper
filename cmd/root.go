package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/downloader"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/bunker"
	"github.com/rajbahakdanyu/scraper/internal/parser/cyberdrop"
	"github.com/rajbahakdanyu/scraper/internal/parser/filester"
	"github.com/rajbahakdanyu/scraper/internal/parser/goonbox"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

// globalOptions holds flags defined on the root command that are inherited
// by all subcommands via PersistentFlags.
type globalOptions struct {
	configPath       string
	output           string
	outputSet        bool
	concurrency      int
	concurrencySet   bool
	rateLimit        string
	rateLimitSet     bool
	skipChecksums    bool
	skipChecksumsSet bool
	dryRun           bool
	inputFile        string
	inputFileSet     bool
	noTui            bool
	json             bool
	proxy            string
	proxySet         bool
}

// loadGlobalConfig loads the config file and applies CLI flag overrides.
func loadGlobalConfig(opts globalOptions) (*config.Config, error) {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if opts.outputSet {
		cfg.OutputDir = opts.output
	}
	if opts.concurrencySet {
		cfg.ConcurrentDownloads = opts.concurrency
	}
	if opts.rateLimitSet {
		cfg.RateLimit = opts.rateLimit
	}
	if opts.skipChecksumsSet {
		cfg.SkipChecksums = opts.skipChecksums
	}
	if opts.proxySet {
		cfg.Proxy = opts.proxy
	}

	// Single choke point covering both the --output flag and the config
	// file's output_dir: the flag override above bypasses config.Load's own
	// expansion, and the shell never expands a quoted `-o "~"` value.
	outputDir, err := expandHomePath(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	cfg.OutputDir = outputDir

	return cfg, nil
}

// expandHomePath expands a leading "~" in path to the user's home
// directory: exactly "~" maps to the home dir and "~/..." to a path under
// it. "~user" forms are rejected with a clear error — silently creating a
// directory literally named "~user" is never what the user meant. Paths
// without a leading "~" (relative or absolute) pass through untouched.
func expandHomePath(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return "", fmt.Errorf("cannot expand %q: ~user home directories are not supported", path)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("expand %q: home directory unavailable", path)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func globalOptionsFromCommand(cmd *cobra.Command) (globalOptions, error) {
	flags := cmd.Root().PersistentFlags()
	configPath, err := flags.GetString("config")
	if err != nil {
		return globalOptions{}, err
	}
	output, err := flags.GetString("output")
	if err != nil {
		return globalOptions{}, err
	}
	concurrency, err := flags.GetInt("concurrent")
	if err != nil {
		return globalOptions{}, err
	}
	rateLimit, err := flags.GetString("limit-rate")
	if err != nil {
		return globalOptions{}, err
	}
	skipChecksums, err := flags.GetBool("no-checksums")
	if err != nil {
		return globalOptions{}, err
	}
	dryRun, err := flags.GetBool("dry-run")
	if err != nil {
		return globalOptions{}, err
	}
	inputFile, err := flags.GetString("input-file")
	if err != nil {
		return globalOptions{}, err
	}
	noTui, err := flags.GetBool("no-tui")
	if err != nil {
		return globalOptions{}, err
	}
	json, err := flags.GetBool("json")
	if err != nil {
		return globalOptions{}, err
	}
	proxy, err := flags.GetString("proxy")
	if err != nil {
		return globalOptions{}, err
	}
	// --json implies --no-tui
	if json {
		noTui = true
	}
	return globalOptions{
		configPath:       configPath,
		output:           output,
		outputSet:        flags.Changed("output"),
		concurrency:      concurrency,
		concurrencySet:   flags.Changed("concurrent"),
		rateLimit:        rateLimit,
		rateLimitSet:     flags.Changed("limit-rate"),
		skipChecksums:    skipChecksums,
		skipChecksumsSet: flags.Changed("no-checksums"),
		dryRun:           dryRun,
		inputFile:        inputFile,
		inputFileSet:     flags.Changed("input-file"),
		noTui:            noTui,
		json:             json,
		proxy:            proxy,
		proxySet:         flags.Changed("proxy"),
	}, nil
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "scraper [url...]",
		Short: "Media scraper CLI tool",
		Long:  `Download media from various sites with a beautiful terminal UI.`,
		// Runtime errors (bad URL, network failure, etc.) shouldn't dump the
		// full usage/help text — just cobra's own clean "Error: ..." line.
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}

			urls, err := collectBatchURLs(args, opts)
			if err != nil {
				return err
			}

			if len(urls) == 1 && !opts.inputFileSet {
				targetURL := urls[0]
				site, err := detectSite(targetURL)
				if err != nil {
					return err
				}
				return runWithScraper(scraperRegistry()[site], targetURL, site, opts)
			}
			return runBatch(opts, urls)
		},
	}

	root.PersistentFlags().String("config", "", "Config file path (default: ~/.scraper.yaml)")
	root.PersistentFlags().StringP("output", "o", ".", "Output directory")
	root.PersistentFlags().Int("concurrent", 5, "Maximum concurrent downloads")
	root.PersistentFlags().String("limit-rate", "", "Cap total download speed (e.g. 500K, 2M, 1G; default: unlimited)")
	root.PersistentFlags().Bool("no-checksums", false, "Don't write/verify .sha256 checksum sidecar files")
	root.PersistentFlags().BoolP("dry-run", "n", false, "List items and total size without downloading anything")
	root.PersistentFlags().StringP("input-file", "i", "", "Read URLs from a file (one per line)")
	root.PersistentFlags().Bool("no-tui", false, "Disable terminal UI; print plain text output")
	root.PersistentFlags().BoolP("json", "j", false, "Output JSON Lines (one event per line); implies --no-tui")
	root.PersistentFlags().String("proxy", "", "HTTP/S proxy URL for all requests (e.g. http://proxy:8080)")

	root.AddCommand(
		newYouTubeCmd(),
		newCyberDropCmd(),
		newBunkerCmd(),
		newGoonBoxCmd(),
		newFilesterCmd(),
	)

	return root
}

// scraperRegistry maps a site name to its scraper, used by detectSite's
// auto-detect path.
func scraperRegistry() map[string]model.Scraper {
	return map[string]model.Scraper{
		"youtube":   youtube.NewYouTubeScraper(),
		"cyberdrop": cyberdrop.NewCyberDropScraper(),
		"bunker":    bunker.NewBunkerScraper(),
		"goonbox":   goonbox.NewGoonBoxScraper(),
		"filester":  filester.NewFilesterScraper(),
	}
}

// resolveURL extracts the target URL from positional args or the --url flag.
func resolveURL(args []string, urlFlag, siteName string) (string, error) {
	targetURL := urlFlag
	if len(args) > 0 {
		targetURL = args[0]
	}
	if targetURL == "" {
		return "", fmt.Errorf("a %s URL is required (positional argument or --url)", siteName)
	}
	return targetURL, nil
}

// extractSlug gets the last path segment from a URL, used for naming
// album subfolders.
func extractSlug(rawURL string) string {
	// Remove query string
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if last == "" {
		return ""
	}
	return last
}

// albumSlugFor picks the subfolder name for a multi-item download. YouTube
// playlist URLs don't have a unique path segment (they're all just
// "/playlist?list=..."), so extractSlug's generic "last path segment"
// logic would put every playlist in the same "playlist" folder — use the
// playlist ID instead for those.
func albumSlugFor(siteName, targetURL string) string {
	if siteName == "youtube" {
		if playlistID := youtube.ExtractPlaylistID(targetURL); playlistID != "" {
			return playlistID
		}
	}
	return extractSlug(targetURL)
}

// siteDomains maps each supported scraper to the known domains it operates
// under. Cyberlocker-style sites (Bunkr, CyberDrop, GoonBox) rotate through
// many mirror domains over time, usually in response to takedowns, so this
// list is necessarily incomplete — add new mirrors here as they show up.
var siteDomains = map[string][]string{
	"youtube": {"youtube.com", "youtu.be"},
	"cyberdrop": {
		"cyberdrop.cr", "cyberdrop.me", "cyberdrop.to", "cyberdrop.cc",
	},
	"bunker": {
		"bunkr.cr", "bunkr.su", "bunkrr.su", "bunkr.si", "bunkr.la",
		"bunkr.is", "bunkr.to", "bunkr.ru", "bunkr.black", "bunkr.red",
		"bunkr.ph", "bunkr.site", "bunkr.fi",
	},
	"goonbox":  {"goonbox.cr", "goonbox.to"},
	"filester": {"filester.sh", "filester.me"},
}

// detectSite guesses which scraper module handles rawURL based on its host,
// matching against siteDomains rather than a plain substring so a lookalike
// host (e.g. "notcyberdrop.example.com") can't be mistaken for the real site.
func detectSite(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("could not parse %q as a URL", rawURL)
	}
	host := strings.ToLower(u.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	for site, domains := range siteDomains {
		for _, domain := range domains {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return site, nil
			}
		}
	}
	return "", fmt.Errorf("could not auto-detect a site for %q (available: youtube, cyberdrop, bunker, goonbox, filester)", rawURL)
}

// runWithScraper launches the TUI immediately. The scraper runs in the
// background; the TUI shows a loading spinner until results arrive.
func runWithScraper(scraper model.Scraper, targetURL string, siteName string, opts globalOptions) error {
	cfg, err := loadGlobalConfig(opts)
	if err != nil {
		return err
	}

	if opts.dryRun {
		if opts.json {
			return runDryRunJSON(scraper, targetURL, siteName, os.Stdout)
		}
		return runDryRun(scraper, targetURL)
	}
	rateLimitBytesPerSec, err := config.ParseRateLimit(cfg.RateLimit)
	if err != nil {
		return err
	}

	var dl dashboard.Downloader
	if siteName == "youtube" {
		ytScraper, ok := scraper.(*youtube.Scraper)
		if !ok {
			return fmt.Errorf("youtube: unexpected scraper type %T", scraper)
		}
		dl = youtube.NewDownloaderWithConfig(ytScraper, youtube.DownloaderConfig{
			Concurrency:  cfg.ConcurrentDownloads,
			Timeout:      cfg.Timeout,
			UserAgent:    cfg.UserAgent,
			RateLimit:    rateLimitBytesPerSec,
			SkipChecksum: cfg.SkipChecksums,
			ProxyURL:     cfg.Proxy,
		})
	} else {
		genericDl := downloader.NewDownloaderWithConfig(
			cfg.ConcurrentDownloads,
			cfg.Timeout,
			cfg.UserAgent,
			cfg.Proxy,
		)
		genericDl.SetRateLimit(rateLimitBytesPerSec)
		genericDl.SetSkipChecksums(cfg.SkipChecksums)
		dl = genericDl
	}

	// The scraper function runs in a background goroutine.
	scraperFn := func() ([]model.MediaItem, error) {
		return scraper.Parse(targetURL)
	}

	slug := albumSlugFor(siteName, targetURL)

	// When stdout is not a terminal (piped, redirected, cron), the
	// bubbletea TUI would emit escape-code garbage — fall back to plain
	// line-based output instead.
	if opts.json {
		return runJSON(dl, scraperFn, cfg.OutputDir, slug, siteName, targetURL, os.Stdout)
	}

	if opts.noTui || !term.IsTerminal(int(os.Stdout.Fd())) {
		return runPlain(dl, scraperFn, cfg.OutputDir, slug, os.Stdout)
	}

	d := dashboard.New(scraperFn, dl, cfg.OutputDir, siteName)

	// Pass the URL slug so the dashboard can create a subfolder for albums.
	d.SetAlbumSlug(slug)

	p := tea.NewProgram(d, tea.WithAltScreen())
	d.SetSendFunc(func(msg tea.Msg) { p.Send(msg) })

	_, err = p.Run()
	return err
}

// batchAlbum holds a scraped album's items and metadata.
type batchAlbum struct {
	URL      string
	Site     string
	Slug     string
	Dest     string
	Items    []model.MediaItem
	Err      error
	Scraper  model.Scraper
}

// collectBatchURLs gathers URLs from positional arguments, --input-file, and
// stdin (when not a TTY and no other sources). Lines starting with "#" are
// treated as comments and blank lines are skipped.
func collectBatchURLs(args []string, opts globalOptions) ([]string, error) {
	var urls []string
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			urls = append(urls, arg)
		}
	}

	if opts.inputFileSet {
		fileURLs, err := readURLsFromFile(opts.inputFile)
		if err != nil {
			return nil, err
		}
		urls = append(urls, fileURLs...)
	}

	if len(urls) == 0 && !term.IsTerminal(int(os.Stdin.Fd())) {
		stdinURLs, err := readURLsFromReader(os.Stdin)
		if err != nil {
			return nil, err
		}
		urls = append(urls, stdinURLs...)
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs provided (use positional args, --input-file, or stdin)")
	}
	return urls, nil
}

func readURLsFromFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open input file %q: %w", path, err)
	}
	defer f.Close()
	return readURLsFromReader(f)
}

func readURLsFromReader(r io.Reader) ([]string, error) {
	var urls []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read URLs: %w", err)
	}
	return urls, nil
}

// runBatch orchestrates scraping multiple album URLs with a shared download
// queue. It scrapes albums sequentially — feeding items to the downloader as
// soon as each album finishes — so token-based URLs from early albums don't
// expire before later albums are scraped.
func runBatch(opts globalOptions, urls []string) error {
	if opts.dryRun {
		if opts.json {
			var allErrs []error
			for _, u := range urls {
				site, sitErr := detectSite(u)
				if sitErr != nil {
					allErrs = append(allErrs, sitErr)
					continue
				}
				if jsonErr := runDryRunJSON(scraperRegistry()[site], u, site, os.Stdout); jsonErr != nil {
					allErrs = append(allErrs, jsonErr)
				}
			}
			return model.NewPartialError("batch dry-run", allErrs)
		}
		var errs []error
		for _, u := range urls {
			site, sitErr := detectSite(u)
			if sitErr != nil {
				errs = append(errs, sitErr)
				continue
			}
			if dryErr := runDryRun(scraperRegistry()[site], u); dryErr != nil {
				errs = append(errs, dryErr)
			}
		}
		return model.NewPartialError("batch dry-run", errs)
	}

	hasYouTube, hasOther := false, false
	for _, u := range urls {
		site, err := detectSite(u)
		if err != nil {
			return err
		}
		if site == "youtube" {
			hasYouTube = true
		} else {
			hasOther = true
		}
	}
	if hasYouTube && hasOther {
		return fmt.Errorf("batch mode only supports URLs from the same site type (all YouTube or all non-YouTube)")
	}

	cfg, err := loadGlobalConfig(opts)
	if err != nil {
		return err
	}

	rateLimitBytesPerSec, err := config.ParseRateLimit(cfg.RateLimit)
	if err != nil {
		return err
	}

	var dl dashboard.Downloader
	// Build dashAlbumURL entries for the dashboard: one scraper per URL so
	// the incremental scrape chain can call the right parser.
	dashAlbums := make([]dashboard.DashAlbumURL, 0, len(urls))
	if hasYouTube {
		scraper := youtube.NewYouTubeScraper()
		dl = youtube.NewDownloaderWithConfig(scraper, youtube.DownloaderConfig{
			Concurrency:  cfg.ConcurrentDownloads,
			Timeout:      cfg.Timeout,
			UserAgent:    cfg.UserAgent,
			RateLimit:    rateLimitBytesPerSec,
			SkipChecksum: cfg.SkipChecksums,
			ProxyURL:     cfg.Proxy,
		})
		for _, u := range urls {
			slug := albumSlugFor("youtube", u)
			dest, err := fileutil.SafeJoin(cfg.OutputDir, slug)
			if err != nil {
				return err
			}
			dashAlbums = append(dashAlbums, dashboard.DashAlbumURL{
				Site:     "youtube",
				URL:      u,
				Slug:     slug,
				Dest:     dest,
				Scraper:  scraper,
			})
		}
	} else {
		genericDl := downloader.NewDownloaderWithConfig(
			cfg.ConcurrentDownloads,
			cfg.Timeout,
			cfg.UserAgent,
			cfg.Proxy,
		)
		genericDl.SetRateLimit(rateLimitBytesPerSec)
		genericDl.SetSkipChecksums(cfg.SkipChecksums)
		dl = genericDl
		for _, u := range urls {
			site, _ := detectSite(u)
			slug := albumSlugFor(site, u)
			dest, err := fileutil.SafeJoin(cfg.OutputDir, slug)
			if err != nil {
				return err
			}
			scraper := scraperRegistry()[site]
			dashAlbums = append(dashAlbums, dashboard.DashAlbumURL{
				Site:     site,
				URL:      u,
				Slug:     slug,
				Dest:     dest,
				Scraper:  scraper,
			})
		}
	}

	if opts.json {
		return runBatchJSON(dl, dashAlbums, cfg.OutputDir, os.Stdout)
	}

	if opts.noTui || !term.IsTerminal(int(os.Stdout.Fd())) {
		return runBatchPlain(dl, dashAlbums, cfg.OutputDir, os.Stdout)
	}

	d := dashboard.NewBatch(dl, cfg.OutputDir, dashAlbums)

	p := tea.NewProgram(d, tea.WithAltScreen())
	d.SetSendFunc(func(msg tea.Msg) { p.Send(msg) })

	_, err = p.Run()
	return err
}

// runDryRun scrapes targetURL and prints the item count and total size
// without downloading or writing anything to disk.
func runDryRun(scraper model.Scraper, targetURL string) error {
	fmt.Printf("Scraping %s...\n", targetURL)

	items, err := scraper.Parse(targetURL)
	if err != nil && len(items) == 0 {
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	if len(items) == 0 {
		fmt.Println("No items found.")
		return nil
	}

	var totalSize int64
	var unknownSize int
	for _, item := range items {
		title := item.Title
		if title == "" {
			title = item.URL
		}
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

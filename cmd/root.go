package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/disk"
	"github.com/rajbahakdanyu/scraper/internal/downloader"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
	"github.com/rajbahakdanyu/scraper/internal/notify"
	"github.com/rajbahakdanyu/scraper/internal/parser/bunker"
	"github.com/rajbahakdanyu/scraper/internal/parser/cyberdrop"
	"github.com/rajbahakdanyu/scraper/internal/parser/filester"
	"github.com/rajbahakdanyu/scraper/internal/parser/goonbox"
	"github.com/rajbahakdanyu/scraper/internal/parser/pixeldrain"
	"github.com/rajbahakdanyu/scraper/internal/parser/turbo"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

var version = "dev"

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
	cookies          string
	cookiesSet       bool
	quality          string
	qualitySet       bool
	audioOnly        string
	audioOnlySet     bool
	include          []string
	includeSet       bool
	exclude          []string
	excludeSet       bool
	minSize          string
	minSizeSet       bool
	maxSize          string
	maxSizeSet       bool
	diskCheck        bool
	diskCheckSet     bool
	notify           bool
	notifySet        bool
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
	if opts.cookiesSet {
		cfg.Cookies = opts.cookies
	}
	if opts.qualitySet {
		cfg.Quality = opts.quality
	}
	if opts.audioOnlySet {
		cfg.AudioOnly = opts.audioOnly
	}
	if opts.audioOnlySet && cfg.AudioOnly == "" {
		cfg.AudioOnly = "m4a"
	}
	if cfg.Quality != "" && cfg.AudioOnly != "" {
		return nil, fmt.Errorf("cannot use --quality and --audio-only together")
	}
	if cfg.Quality != "" {
		if _, ok := youtube.ValidQuality(cfg.Quality); !ok {
			return nil, fmt.Errorf("invalid --quality value %q (valid: 2160p, 1440p, 1080p, 720p, 480p, 360p, 240p)", cfg.Quality)
		}
	}
	if cfg.AudioOnly != "" {
		if cfg.AudioOnly != "m4a" && cfg.AudioOnly != "mp3" {
			return nil, fmt.Errorf("invalid --audio-only value %q (valid: m4a, mp3)", cfg.AudioOnly)
		}
	}
	if opts.includeSet {
		cfg.Include = opts.include
	}
	if opts.excludeSet {
		cfg.Exclude = opts.exclude
	}
	if opts.minSizeSet {
		cfg.MinSize = opts.minSize
	}
	if opts.maxSizeSet {
		cfg.MaxSize = opts.maxSize
	}
	if opts.diskCheckSet {
		cfg.DiskCheck = opts.diskCheck
	}
	if opts.notifySet {
		cfg.Notify = opts.notify
	}

	for i, p := range cfg.Include {
		if _, err := filepath.Match(p, "test"); err != nil {
			return nil, fmt.Errorf("invalid --include pattern %q (position %d): %w", p, i+1, err)
		}
	}
	for i, p := range cfg.Exclude {
		if _, err := filepath.Match(p, "test"); err != nil {
			return nil, fmt.Errorf("invalid --exclude pattern %q (position %d): %w", p, i+1, err)
		}
	}

	if cfg.MinSize != "" {
		n, err := config.ParseSize(cfg.MinSize)
		if err != nil {
			return nil, fmt.Errorf("invalid --min-size: %w", err)
		}
		cfg.SetMinSizeBytes(n)
	}
	if cfg.MaxSize != "" {
		n, err := config.ParseSize(cfg.MaxSize)
		if err != nil {
			return nil, fmt.Errorf("invalid --max-size: %w", err)
		}
		cfg.SetMaxSizeBytes(n)
	}
	if cfg.Cookies != "" {
		expandedCookies, err := config.ExpandHomePath(cfg.Cookies)
		if err != nil {
			return nil, err
		}
		cfg.Cookies = expandedCookies
		parsed, err := netutil.ParseNetscapeCookies(cfg.Cookies)
		if err != nil {
			return nil, fmt.Errorf("cookies: %w", err)
		}
		cfg.SetParsedCookies(parsed)
	}

	// Single choke point covering both the --output flag and the config
	// file's output_dir: the flag override above bypasses config.Load's own
	// expansion, and the shell never expands a quoted `-o "~"` value.
	outputDir, err := config.ExpandHomePath(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	cfg.OutputDir = outputDir

	return cfg, nil
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
	cookies, err := flags.GetString("cookies")
	if err != nil {
		return globalOptions{}, err
	}
	quality, err := flags.GetString("quality")
	if err != nil {
		return globalOptions{}, err
	}
	audioOnly, err := flags.GetString("audio-only")
	if err != nil {
		return globalOptions{}, err
	}
	include, err := flags.GetStringSlice("include")
	if err != nil {
		return globalOptions{}, err
	}
	exclude, err := flags.GetStringSlice("exclude")
	if err != nil {
		return globalOptions{}, err
	}
	minSize, err := flags.GetString("min-size")
	if err != nil {
		return globalOptions{}, err
	}
	maxSize, err := flags.GetString("max-size")
	if err != nil {
		return globalOptions{}, err
	}
	diskCheck, err := flags.GetBool("disk-check")
	if err != nil {
		return globalOptions{}, err
	}
	notify, err := flags.GetBool("notify")
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
		cookies:          cookies,
		cookiesSet:       flags.Changed("cookies"),
		quality:          quality,
		qualitySet:       flags.Changed("quality"),
		audioOnly:        audioOnly,
		audioOnlySet:     flags.Changed("audio-only"),
		include:          include,
		includeSet:       flags.Changed("include"),
		exclude:          exclude,
		excludeSet:       flags.Changed("exclude"),
		minSize:          minSize,
		minSizeSet:       flags.Changed("min-size"),
		maxSize:          maxSize,
		maxSizeSet:       flags.Changed("max-size"),
		diskCheck:        diskCheck,
		diskCheckSet:     flags.Changed("disk-check"),
		notify:           notify,
		notifySet:        flags.Changed("notify"),
	}, nil
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newSiteCmd(use, short, site string, newScraper func() model.Scraper) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, site)
			if err != nil {
				return err
			}
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			return runWithScraper(newScraper(), targetURL, site, opts)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", site+" URL")
	return cmd
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
				cfg, err := loadGlobalConfig(opts)
				if err != nil {
					return err
				}
				reg, err := scraperRegistry(cfg.Proxy, nil)
				if err != nil {
					return err
				}
				return runWithScraper(reg[site], targetURL, site, opts)
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
	root.PersistentFlags().String("cookies", "", "Netscape-format cookies file (e.g. ~/cookies.txt)")
	root.PersistentFlags().String("quality", "", "Max YouTube resolution (2160p, 1440p, 1080p, 720p, 480p, 360p, 240p)")
	root.PersistentFlags().String("audio-only", "", "YouTube audio only; optional format: m4a (default) or mp3")
	root.PersistentFlags().StringSlice("include", nil, "Keep only items matching glob (repeatable, e.g. -i '*.mp4' -i '*.mkv')")
	root.PersistentFlags().StringSlice("exclude", nil, "Skip items matching glob (repeatable)")
	root.PersistentFlags().String("min-size", "", "Skip items smaller than size (e.g. 500K, 2M)")
	root.PersistentFlags().String("max-size", "", "Skip items larger than size (e.g. 500M, 1G)")
	root.PersistentFlags().Bool("disk-check", true, "Warn if free disk space is less than estimated download size")
	root.PersistentFlags().Bool("notify", false, "Send OS notification when downloads complete")

	root.AddCommand(
		newSiteCmd("youtube <url>", "Scrape and download a YouTube video or playlist", "youtube", func() model.Scraper { return youtube.NewYouTubeScraper() }),
		newSiteCmd("cyberdrop <url>", "Scrape and download a CyberDrop gallery", "cyberdrop", func() model.Scraper { return cyberdrop.NewCyberDropScraper() }),
		newSiteCmd("bunker <url>", "Scrape and download Bunker content", "bunker", func() model.Scraper { return bunker.NewBunkerScraper() }),
		newSiteCmd("goonbox <url>", "Scrape and download a GoonBox album", "goonbox", func() model.Scraper { return goonbox.NewGoonBoxScraper() }),
		newSiteCmd("filester <url>", "Scrape and download a Filester file or folder", "filester", func() model.Scraper { return filester.NewFilesterScraper() }),
		newSiteCmd("pixeldrain <url>", "Scrape and download a Pixeldrain list or single file", "pixeldrain", func() model.Scraper { return pixeldrain.NewPixeldrainScraper() }),
		newSiteCmd("turbo <url>", "Scrape and download a Turbo album", "turbo", func() model.Scraper { return turbo.NewTurboScraper() }),
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Println("scraper", version)
			},
		},
		&cobra.Command{
			Use:   "completion [bash|zsh|fish|powershell]",
			Short: "Generate shell completion script",
			Long: `To load completions:

Bash:  source <(scraper completion bash)
Zsh:   source <(scraper completion zsh)
Fish:  scraper completion fish | source`,
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				switch args[0] {
				case "bash":
					return cmd.Root().GenBashCompletion(os.Stdout)
				case "zsh":
					return cmd.Root().GenZshCompletion(os.Stdout)
				case "fish":
					return cmd.Root().GenFishCompletion(os.Stdout, true)
				case "powershell":
					return cmd.Root().GenPowerShellCompletion(os.Stdout)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "invalid completion shell %q\n\n%s", args[0], cmd.Root().UsageString())
				return fmt.Errorf("invalid completion shell %q", args[0])
			},
		},
	)

	return root
}

// scraperRegistry maps a site name to its scraper, used by detectSite's
// auto-detect path.
func scraperRegistry(proxyURL string, cookies []*http.Cookie) (map[string]model.Scraper, error) {
	reg := map[string]model.Scraper{
		"youtube": youtube.NewYouTubeScraper(),
	}
	constructors := []struct {
		name    string
		plain   func() model.Scraper
		cookies func([]*http.Cookie) model.Scraper
		proxy   func(string) (model.Scraper, error)
	}{
		{"cyberdrop", func() model.Scraper { return cyberdrop.NewCyberDropScraper() }, func(c []*http.Cookie) model.Scraper { return cyberdrop.NewCyberDropScraperWithCookies(c) }, func(p string) (model.Scraper, error) { return cyberdrop.NewCyberDropScraperWithProxy(p) }},
		{"bunker", func() model.Scraper { return bunker.NewBunkerScraper() }, func(c []*http.Cookie) model.Scraper { return bunker.NewBunkerScraperWithCookies(c) }, func(p string) (model.Scraper, error) { return bunker.NewBunkerScraperWithProxy(p) }},
		{"goonbox", func() model.Scraper { return goonbox.NewGoonBoxScraper() }, func(c []*http.Cookie) model.Scraper { return goonbox.NewGoonBoxScraperWithCookies(c) }, func(p string) (model.Scraper, error) { return goonbox.NewGoonBoxScraperWithProxy(p) }},
		{"filester", func() model.Scraper { return filester.NewFilesterScraper() }, func(c []*http.Cookie) model.Scraper { return filester.NewFilesterScraperWithCookies(c) }, func(p string) (model.Scraper, error) { return filester.NewFilesterScraperWithProxy(p) }},
		{"pixeldrain", func() model.Scraper { return pixeldrain.NewPixeldrainScraper() }, func(c []*http.Cookie) model.Scraper { return pixeldrain.NewPixeldrainScraperWithCookies(c) }, func(p string) (model.Scraper, error) { return pixeldrain.NewPixeldrainScraperWithProxy(p) }},
		{"turbo", func() model.Scraper { return turbo.NewTurboScraper() }, func(c []*http.Cookie) model.Scraper { return turbo.NewTurboScraperWithCookies(c) }, func(p string) (model.Scraper, error) { return turbo.NewTurboScraperWithProxy(p) }},
	}
	for _, constructors := range constructors {
		var scraper model.Scraper
		var err error
		if proxyURL != "" {
			scraper, err = constructors.proxy(proxyURL)
			if err != nil {
				return nil, err
			}
			if len(cookies) > 0 {
				scraper.(interface{ SetCookies([]*http.Cookie) }).SetCookies(cookies)
			}
		} else if len(cookies) > 0 {
			scraper = constructors.cookies(cookies)
		} else {
			scraper = constructors.plain()
		}
		reg[constructors.name] = scraper
	}
	return reg, nil
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
	return netutil.PathSlug(rawURL)
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
		"bunkr.ph", "bunkr.site", "bunkr.fi", "bunkr.pk", "bunkr.ac",
	},
	"goonbox":    {"goonbox.cr", "goonbox.to"},
	"filester":   {"filester.sh", "filester.me"},
	"pixeldrain": {"pixeldrain.com"},
	"turbo":      {"turbo.cr"},
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
	return "", fmt.Errorf("could not auto-detect a site for %q (available: youtube, cyberdrop, bunker, goonbox, filester, pixeldrain, turbo)", rawURL)
}

// runWithScraper launches the TUI immediately. The scraper runs in the
// background; the TUI shows a loading spinner until results arrive.
func runWithScraper(scraper model.Scraper, targetURL string, siteName string, opts globalOptions) error {
	cfg, err := loadGlobalConfig(opts)
	if err != nil {
		return err
	}

	if len(cfg.ParsedCookies()) > 0 {
		if s, ok := scraper.(interface{ SetCookies([]*http.Cookie) }); ok {
			s.SetCookies(cfg.ParsedCookies())
		}
	}

	if opts.dryRun {
		if opts.json {
			return runDryRunJSONWithDiskCheck(scraper, targetURL, siteName, os.Stdout, cfg.OutputDir, cfg.DiskCheck)
		}
		return runDryRunWithDiskCheck(scraper, targetURL, cfg.OutputDir, cfg.DiskCheck)
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

	// The scraper function runs in a background goroutine.
	scraperFn := func(ctx context.Context) ([]model.MediaItem, error) {
		items, err := scraper.Parse(ctx, targetURL)
		if err != nil {
			return nil, err
		}
		if cfg.HasFilters() {
			items, summary := model.FilterItems(items, cfg.Include, cfg.Exclude, cfg.MinSizeBytes(), cfg.MaxSizeBytes())
			fmt.Fprintf(os.Stderr, "filters: %s\n", summary)
			if len(items) == 0 {
				fmt.Fprintln(os.Stderr, "No items matched filters.")
			}
		}

		runDiskCheck(items, cfg.OutputDir, cfg.DiskCheck)

		return items, nil
	}

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
			}, cfg.OutputDir, slug, os.Stdout)
		},
		func() error {
			d := dashboard.New(scraperFn, dl, cfg.OutputDir, siteName)
			d.SetAlbumSlug(slug)

			p := tea.NewProgram(d, tea.WithAltScreen())

			_, err := p.Run()
			return err
		},
	)
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
			SkipChecksum: cfg.SkipChecksums,
			ProxyURL:     cfg.Proxy,
			Cookies:      cfg.ParsedCookies(),
			Quality:      cfg.Quality,
			AudioOnly:    cfg.AudioOnly,
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
	dl.SetSkipChecksums(cfg.SkipChecksums)
	return dl, nil
}

func finishRun(cfg *config.Config, opts globalOptions, jsonRun, plainRun, tuiRun func() error) error {
	var err error
	if opts.json {
		err = jsonRun()
	} else if opts.noTui || !term.IsTerminal(int(os.Stdout.Fd())) {
		err = plainRun()
	} else {
		err = tuiRun()
	}
	if cfg.Notify {
		tryNotify(cfg, "Download complete")
	}
	return err
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
	f, err := os.Open(filepath.Clean(path))
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
	cfg, err := loadGlobalConfig(opts)
	if err != nil {
		return err
	}

	reg, err := batchScraperRegistry(cfg.Proxy, cfg.ParsedCookies())
	if err != nil {
		return err
	}

	if opts.dryRun {
		if opts.json {
			var allErrs []error
			for _, u := range urls {
				site, sitErr := detectSite(u)
				if sitErr != nil {
					allErrs = append(allErrs, sitErr)
					continue
				}
				if jsonErr := runDryRunJSONWithDiskCheck(reg[site], u, site, os.Stdout, cfg.OutputDir, cfg.DiskCheck); jsonErr != nil {
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
			if dryErr := runDryRunWithDiskCheck(reg[site], u, cfg.OutputDir, cfg.DiskCheck); dryErr != nil {
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

	rateLimitBytesPerSec, err := config.ParseRateLimit(cfg.RateLimit)
	if err != nil {
		return err
	}

	var dl dashboard.Downloader
	// Build dashAlbumURL entries for the dashboard: one scraper per URL so
	// the incremental scrape chain can call the right parser.
	dashAlbums := make([]dashboard.DashAlbumURL, 0, len(urls))
	if hasYouTube {
		scraper := batchYouTubeScraper()
		ytDl, err := batchYouTubeDownloader(scraper, cfg, rateLimitBytesPerSec)
		if err != nil {
			return fmt.Errorf("youtube downloader: %w", err)
		}
		dl = ytDl
		defer dl.Shutdown()
		for _, u := range urls {
			slug := albumSlugFor("youtube", u)
			dest, err := fileutil.SafeJoin(cfg.OutputDir, slug)
			if err != nil {
				return err
			}
			dashAlbums = append(dashAlbums, dashboard.DashAlbumURL{
				Site:    "youtube",
				URL:     u,
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
		for _, u := range urls {
			site, _ := detectSite(u)
			slug := albumSlugFor(site, u)
			dest, err := fileutil.SafeJoin(cfg.OutputDir, slug)
			if err != nil {
				return err
			}
			scraper := reg[site]
			dashAlbums = append(dashAlbums, dashboard.DashAlbumURL{
				Site:    site,
				URL:     u,
				Slug:    slug,
				Dest:    dest,
				Scraper: scraper,
			})
		}
	}

	if cfg.HasFilters() || cfg.DiskCheck {
		for i := range dashAlbums {
			dashAlbums[i].Scraper = &filterScraper{
				inner:     dashAlbums[i].Scraper,
				include:   cfg.Include,
				exclude:   cfg.Exclude,
				minSize:   cfg.MinSizeBytes(),
				maxSize:   cfg.MaxSizeBytes(),
				diskCheck: cfg.DiskCheck,
				outputDir: cfg.OutputDir,
			}
		}
	}

	return finishRun(cfg, opts,
		func() error {
			return runBatchJSON(dl, dashAlbums, cfg.OutputDir, cfg.Quality, cfg.AudioOnly, os.Stdout)
		},
		func() error {
			return runBatchPlain(dl, dashAlbums, cfg.OutputDir, os.Stdout)
		},
		func() error {
			d := dashboard.NewBatch(dl, cfg.OutputDir, dashAlbums)

			p := tea.NewProgram(d, tea.WithAltScreen())

			_, err := p.Run()
			return err
		},
	)
}

var batchScraperRegistry = scraperRegistry

var batchYouTubeScraper = func() model.Scraper {
	return youtube.NewYouTubeScraper()
}

var batchYouTubeDownloader = func(scraper model.Scraper, cfg *config.Config, rateLimitBytesPerSec int64) (dashboard.Downloader, error) {
	return buildDownloader(scraper, "youtube", cfg, rateLimitBytesPerSec)
}

var batchGenericDownloader = func(cfg *config.Config, rateLimitBytesPerSec int64) (dashboard.Downloader, error) {
	return buildDownloader(nil, "generic", cfg, rateLimitBytesPerSec)
}

func runDryRunWithDiskCheck(scraper model.Scraper, targetURL, outputDir string, diskCheck bool) error {
	fmt.Printf("Scraping %s...\n", targetURL)

	items, err := scraper.Parse(context.Background(), targetURL)
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
	diskCheck        bool
	outputDir        string
	diskChecked      bool
}

func (f *filterScraper) Name() string { return f.inner.Name() }

func (f *filterScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	items, err := f.inner.Parse(ctx, url)
	if err != nil {
		return nil, err
	}
	filtered := items
	if len(f.include) > 0 || len(f.exclude) > 0 || f.minSize > 0 || f.maxSize > 0 {
		var summary model.FilterSummary
		filtered, summary = model.FilterItems(items, f.include, f.exclude, f.minSize, f.maxSize)
		fmt.Fprintf(os.Stderr, "filters: %s\n", summary)
		if len(filtered) == 0 {
			fmt.Fprintln(os.Stderr, "No items matched filters.")
		}
	}
	if f.diskCheck && !f.diskChecked {
		f.diskChecked = true
		runDiskCheck(filtered, f.outputDir, true)
	}
	return filtered, nil
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
	free, err := disk.FreeSpace(outputDir)
	if err == nil && free < total {
		fmt.Fprintln(os.Stderr, diskWarning(total, free, unknown))
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

func tryNotify(cfg *config.Config, summary string) {
	if cfg.Notify {
		if err := notify.Send(summary); err != nil {
			fmt.Fprintf(os.Stderr, "notification: %v\n", err)
		}
	}
}

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

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
	configPath            string
	output                string
	outputSet             bool
	concurrency           int
	concurrencySet        bool
	rateLimit             string
	rateLimitSet          bool
	checksums             bool
	checksumsSet          bool
	dryRun                bool
	inputFile             string
	inputFileSet          bool
	noTui                 bool
	json                  bool
	proxy                 string
	proxySet              bool
	cookies               string
	cookiesSet            bool
	quality               string
	qualitySet            bool
	audioOnly             string
	audioOnlySet          bool
	include               []string
	includeSet            bool
	exclude               []string
	excludeSet            bool
	allowExt              []string
	allowExtSet           bool
	maxAlbumItems         int
	maxAlbumItemsSet      bool
	scrapeDelay           time.Duration
	scrapeDelaySet        bool
	logFile               string
	logFileSet            bool
	printURLs             bool
	watch                 time.Duration
	cookiesFromBrowser    string
	cookiesFromBrowserSet bool
	minSize               string
	minSizeSet            bool
	maxSize               string
	maxSizeSet            bool
	diskCheck             bool
	diskCheckSet          bool
	mediaOnly             bool
	mediaOnlySet          bool
	notify                bool
	notifySet             bool
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
	if cfg.ConcurrentDownloads < 1 || cfg.ConcurrentDownloads > 256 {
		return nil, fmt.Errorf("invalid --concurrent %d (must be between 1 and 256)", cfg.ConcurrentDownloads)
	}
	if opts.rateLimitSet {
		cfg.RateLimit = opts.rateLimit
	}
	if opts.checksumsSet {
		cfg.Checksums = opts.checksums
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
	if opts.allowExtSet {
		cfg.MediaExtensions = opts.allowExt
	}
	if opts.maxAlbumItemsSet {
		cfg.MaxAlbumItems = opts.maxAlbumItems
	}
	if opts.scrapeDelaySet {
		cfg.ScrapeDelay = opts.scrapeDelay
	}
	if opts.logFileSet {
		cfg.LogFile = opts.logFile
	}
	if cfg.LogFile != "" {
		if err := openLogFile(cfg.LogFile); err != nil {
			return nil, err
		}
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
	if opts.mediaOnlySet {
		cfg.MediaOnly = opts.mediaOnly
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
	if opts.cookiesFromBrowserSet {
		cfg.CookiesFromBrowser = opts.cookiesFromBrowser
	}
	if cfg.CookiesFromBrowser != "" {
		imported, err := importBrowserCookies(cfg.CookiesFromBrowser)
		if err != nil {
			return nil, err
		}
		cfg.SetParsedCookies(append(cfg.ParsedCookies(), imported...))
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
	checksums, err := flags.GetBool("checksums")
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
	allowExt, err := flags.GetStringSlice("allow-ext")
	if err != nil {
		return globalOptions{}, err
	}
	maxAlbumItems, err := flags.GetInt("max-album-items")
	if err != nil {
		return globalOptions{}, err
	}
	printURLs, err := flags.GetBool("print-urls")
	if err != nil {
		return globalOptions{}, err
	}
	scrapeDelay, err := flags.GetDuration("scrape-delay")
	if err != nil {
		return globalOptions{}, err
	}
	logFilePath, err := flags.GetString("log-file")
	if err != nil {
		return globalOptions{}, err
	}
	watch, err := flags.GetDuration("watch")
	if err != nil {
		return globalOptions{}, err
	}
	cookiesFromBrowser, err := flags.GetString("cookies-from-browser")
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
	mediaOnly, err := flags.GetBool("media-only")
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
		configPath:            configPath,
		output:                output,
		outputSet:             flags.Changed("output"),
		concurrency:           concurrency,
		concurrencySet:        flags.Changed("concurrent"),
		rateLimit:             rateLimit,
		rateLimitSet:          flags.Changed("limit-rate"),
		checksums:             checksums,
		checksumsSet:          flags.Changed("checksums"),
		dryRun:                dryRun,
		inputFile:             inputFile,
		inputFileSet:          flags.Changed("input-file"),
		noTui:                 noTui,
		json:                  json,
		proxy:                 proxy,
		proxySet:              flags.Changed("proxy"),
		cookies:               cookies,
		cookiesSet:            flags.Changed("cookies"),
		quality:               quality,
		qualitySet:            flags.Changed("quality"),
		audioOnly:             audioOnly,
		audioOnlySet:          flags.Changed("audio-only"),
		include:               include,
		includeSet:            flags.Changed("include"),
		exclude:               exclude,
		excludeSet:            flags.Changed("exclude"),
		allowExt:              allowExt,
		allowExtSet:           flags.Changed("allow-ext"),
		maxAlbumItems:         maxAlbumItems,
		maxAlbumItemsSet:      flags.Changed("max-album-items"),
		scrapeDelay:           scrapeDelay,
		scrapeDelaySet:        flags.Changed("scrape-delay"),
		logFile:               logFilePath,
		logFileSet:            flags.Changed("log-file"),
		printURLs:             printURLs,
		watch:                 watch,
		cookiesFromBrowser:    cookiesFromBrowser,
		cookiesFromBrowserSet: flags.Changed("cookies-from-browser"),
		minSize:               minSize,
		minSizeSet:            flags.Changed("min-size"),
		maxSize:               maxSize,
		maxSizeSet:            flags.Changed("max-size"),
		diskCheck:             diskCheck,
		diskCheckSet:          flags.Changed("disk-check"),
		mediaOnly:             mediaOnly,
		mediaOnlySet:          flags.Changed("media-only"),
		notify:                notify,
		notifySet:             flags.Changed("notify"),
	}, nil
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		logLine("error: %v", err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps a run error to a process exit code: 1 when the whole run
// failed, 2 when only part of it did (some items or albums failed while
// others succeeded).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var partial *model.PartialError
	if errors.As(err, &partial) {
		return 2
	}
	return 1
}

// logFile is the optional --log-file sink; nil when unset.
var logFile io.Writer

// noticeSink, when non-nil, receives in-run warnings instead of stderr.
var (
	noticeMu   sync.Mutex
	noticeSink io.Writer
)

// notice prints an in-run warning (filters, caps, disk space). Scrapes run
// while the TUI owns the terminal, so these must not go straight to stderr.
func notice(format string, args ...any) {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	w := noticeSink
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, args...)
}

// withBufferedNotices holds notices while fn owns the terminal and prints
// them once it returns.
func withBufferedNotices(fn func() error) error {
	var buf bytes.Buffer
	noticeMu.Lock()
	noticeSink = &buf
	noticeMu.Unlock()

	err := fn()

	noticeMu.Lock()
	noticeSink = nil
	held := buf.String()
	noticeMu.Unlock()
	fmt.Fprint(os.Stderr, held)
	return err
}

// openLogFile opens (appending) the optional run log. A failure is fatal so
// a typo'd path is caught before any downloads start.
func openLogFile(path string) error {
	expanded, err := config.ExpandHomePath(path)
	if err != nil {
		return err
	}
	f, err := fileutil.OpenFileNoFollow(expanded, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if previous, ok := logFile.(io.Closer); ok {
		_ = previous.Close()
	}
	logFile = f
	return nil
}

// applyWatchDefaults makes watch mode non-interactive: a TUI cannot restart
// itself per interval.
func applyWatchDefaults(opts globalOptions) globalOptions {
	opts.noTui = true
	return opts
}

// watchLoop runs the list once, then re-runs it every interval until
// interrupted. Run failures are reported and the loop keeps watching.
func watchLoop(runOnce func() error, interval time.Duration) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	for {
		if err := runOnce(); err != nil {
			fmt.Fprintf(os.Stderr, "watch: %v\n", err)
			logLine("watch error: %v", err)
		}
		fmt.Fprintf(os.Stderr, "watch: next run in %s (Ctrl+C to stop)\n", interval)
		select {
		case <-signals:
			return nil
		case <-time.After(interval):
		}
	}
}

// logLine writes one timestamped line to the optional run log.
func logLine(format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	fmt.Fprintf(logFile, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func newSiteCmd(use, short, site string) *cobra.Command {
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
			return runSingle(opts, site, targetURL)
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
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			if helpRequested(args, opts.inputFileSet, stdinIsTerminal()) {
				return cmd.Help()
			}

			entries, err := collectBatchURLs(args, opts)
			if err != nil {
				return err
			}

			if opts.watch > 0 {
				opts = applyWatchDefaults(opts)
				return watchLoop(newRunOnce(opts, entries), opts.watch)
			}
			return newRunOnce(opts, entries)()
		},
	}

	root.PersistentFlags().String("config", "", "Config file path (default: ~/.scraper.yaml)")
	root.PersistentFlags().StringP("output", "o", "", "Output directory (default: ~/Downloads)")
	root.PersistentFlags().Int("concurrent", 3, "Maximum concurrent downloads")
	root.PersistentFlags().String("limit-rate", "", "Cap total download speed (e.g. 500K, 2M, 1G; default: unlimited)")
	root.PersistentFlags().Bool("checksums", false, "Write and verify .sha256 checksum sidecar files (default: off)")
	root.PersistentFlags().BoolP("dry-run", "n", false, "List items and total size without downloading anything")
	root.PersistentFlags().StringP("input-file", "i", "", "Read URLs from a file (one per line; optional tab-separated album URL)")
	root.PersistentFlags().Bool("no-tui", false, "Disable terminal UI; print plain text output")
	root.PersistentFlags().BoolP("json", "j", false, "Output JSON Lines (one event per line); implies --no-tui")
	root.PersistentFlags().String("proxy", "", "HTTP/S proxy URL for all requests (e.g. http://proxy:8080)")
	root.PersistentFlags().String("cookies", "", "Netscape-format cookies file (e.g. ~/cookies.txt)")
	root.PersistentFlags().String("quality", "", "Max YouTube resolution (2160p, 1440p, 1080p, 720p, 480p, 360p, 240p)")
	root.PersistentFlags().String("audio-only", "", "YouTube audio only; optional format: m4a (default) or mp3")
	// Bare --audio-only means m4a: the help text calls the format
	// optional, so the flag must work without a value.
	root.PersistentFlags().Lookup("audio-only").NoOptDefVal = "m4a"
	root.PersistentFlags().StringSlice("include", nil, "Keep only items matching glob (repeatable, e.g. --include '*.mp4' --include '*.mkv')")
	root.PersistentFlags().StringSlice("exclude", nil, "Skip items matching glob (repeatable)")
	root.PersistentFlags().StringSlice("allow-ext", nil, "Extra file extensions to keep with --media-only (repeatable, e.g. --allow-ext cbz)")
	root.PersistentFlags().Int("max-album-items", 0, "Cap items per album (0 = unlimited; default 10000)")
	root.PersistentFlags().Bool("print-urls", false, "Print item URLs (one per line) and exit without downloading")
	root.PersistentFlags().Duration("scrape-delay", 0, "Pause between album scrapes (e.g. 500ms; default: none)")
	root.PersistentFlags().String("log-file", "", "Append a timestamped run log (scrape errors, summaries) to this file")
	root.PersistentFlags().Duration("watch", 0, "Re-run the input list every interval until interrupted (implies --no-tui)")
	root.PersistentFlags().String("cookies-from-browser", "", "Import cookies for the supported sites from a local browser profile (firefox)")
	root.PersistentFlags().String("min-size", "", "Skip items smaller than size (e.g. 500K, 2M)")
	root.PersistentFlags().String("max-size", "", "Skip items larger than size (e.g. 500M, 1G)")
	root.PersistentFlags().Bool("disk-check", true, "Warn if free disk space is less than estimated download size")
	root.PersistentFlags().Bool("media-only", true, "Only download media and archive files (use --media-only=false to allow everything)")
	root.PersistentFlags().Bool("notify", false, "Send OS notification when downloads complete")

	root.AddCommand(
		newSiteCmd("youtube <url>", "Scrape and download a YouTube video or playlist", "youtube"),
		newSiteCmd("cyberdrop <url>", "Scrape and download a CyberDrop gallery", "cyberdrop"),
		newSiteCmd("bunker <url>", "Scrape and download Bunker content", "bunker"),
		newSiteCmd("goonbox <url>", "Scrape and download a GoonBox album", "goonbox"),
		newSiteCmd("filester <url>", "Scrape and download a Filester file or folder", "filester"),
		newSiteCmd("pixeldrain <url>", "Scrape and download a Pixeldrain list or single file", "pixeldrain"),
		newSiteCmd("turbo <url>", "Scrape and download a Turbo album", "turbo"),
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
	reg := map[string]model.Scraper{}
	constructors := []struct {
		name    string
		plain   func() model.Scraper
		cookies func([]*http.Cookie) model.Scraper
		proxy   func(string) (model.Scraper, error)
	}{
		{"youtube", func() model.Scraper { return youtube.NewYouTubeScraper() }, func(c []*http.Cookie) model.Scraper {
			s := youtube.NewYouTubeScraper()
			s.SetCookies(c)
			return s
		}, func(p string) (model.Scraper, error) { return youtube.NewYouTubeScraperWithProxy(p) }},
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
// playlist ID instead for those. Plain video URLs all share the "watch"
// path, so fall back to the video ID for those too.
func albumSlugFor(siteName, targetURL string) string {
	if siteName == "youtube" {
		if playlistID := youtube.ExtractPlaylistID(targetURL); playlistID != "" {
			return playlistID
		}
		if videoID := youtube.ExtractVideoID(targetURL); videoID != "" {
			return videoID
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
	"bunker":     bunker.MirrorDomains(),
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

// configLoader is a test seam for loadGlobalConfig.
var configLoader = loadGlobalConfig

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
	reg, err := scraperRegistry(cfg.Proxy, nil)
	if err != nil {
		return err
	}
	return runWithScraperAndConfig(cfg, reg[site], targetURL, site, opts)
}

// runWithScraperAndConfig launches the TUI immediately using an
// already-loaded config, avoiding a second config-file parse/cookie read on
// the root command path. The scraper runs in the background; the TUI shows
// a loading spinner until results arrive.
func runWithScraperAndConfig(cfg *config.Config, scraper model.Scraper, targetURL, siteName string, opts globalOptions) error {
	if len(cfg.ParsedCookies()) > 0 {
		if s, ok := scraper.(interface{ SetCookies([]*http.Cookie) }); ok {
			s.SetCookies(cfg.ParsedCookies())
		}
	}

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

// notificationSummary maps a run's terminal error to the notification text.
func notificationSummary(err error) string {
	if err != nil {
		return "Download failed"
	}
	return "Download complete"
}

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

// runBatch orchestrates scraping multiple album URLs with a shared download
// queue. It scrapes albums sequentially — feeding items to the downloader as
// soon as each album finishes — so token-based URLs from early albums don't
// expire before later albums are scraped.
func runBatch(opts globalOptions, entries []batchEntry) error {
	cfg, err := configLoader(opts)
	if err != nil {
		return err
	}
	return runBatchWithConfig(cfg, opts, entries)
}

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
	var dl dashboard.Downloader
	// Build dashAlbumURL entries for the dashboard: one scraper per URL so
	// the incremental scrape chain can call the right parser.
	dashAlbums := make([]dashboard.DashAlbumURL, 0, len(entries))
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
			dashAlbums = append(dashAlbums, dashboard.DashAlbumURL{
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
			dashAlbums = append(dashAlbums, dashboard.DashAlbumURL{
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

// notifySend is a test seam; production always sends through notify.Send.
var notifySend = notify.Send

func tryNotify(summary string) {
	if err := notifySend(summary); err != nil {
		fmt.Fprintf(os.Stderr, "notification: %v\n", err)
	}
}

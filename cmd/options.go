package cmd

import (
	"fmt"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/spf13/cobra"
)

// globalOptions holds flags defined on the root command that are inherited
// by all subcommands via PersistentFlags.
type globalOptions struct {
	configPath            string
	output                string
	outputSet             bool
	concurrency           int
	concurrencySet        bool
	perHost               int
	perHostSet            bool
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
	youtubeBackend        string
	youtubeBackendSet     bool
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
	if opts.perHostSet {
		cfg.PerHostConcurrency = opts.perHost
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
	if opts.youtubeBackendSet {
		cfg.YouTubeBackend = opts.youtubeBackend
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
	if opts.cookiesFromBrowserSet {
		cfg.CookiesFromBrowser = opts.cookiesFromBrowser
	}

	if err := app.FinalizeConfig(cfg); err != nil {
		return nil, err
	}
	if cfg.LogFile != "" {
		if err := openLogFile(cfg.LogFile); err != nil {
			return nil, err
		}
	}
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
	perHost, err := flags.GetInt("per-host")
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
	youtubeBackend, err := flags.GetString("youtube-backend")
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
		perHost:               perHost,
		perHostSet:            flags.Changed("per-host"),
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
		youtubeBackend:        youtubeBackend,
		youtubeBackendSet:     flags.Changed("youtube-backend"),
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

// applyWatchDefaults makes watch mode non-interactive: a TUI cannot restart
// itself per interval.
func applyWatchDefaults(opts globalOptions) globalOptions {
	opts.noTui = true
	return opts
}

// configLoader is a test seam for loadGlobalConfig.
var configLoader = loadGlobalConfig

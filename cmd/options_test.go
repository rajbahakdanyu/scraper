package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/spf13/cobra"
)

func TestExplicitSubcommandReadsInheritedOptions(t *testing.T) {
	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", "/tmp/custom.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("output", "."); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("concurrent", "5"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("limit-rate", "2M"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("checksums", "true"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("allow-ext", "cbz"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("max-album-items", "5"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("scrape-delay", "250ms"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("log-file", "/tmp/run.log"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}

	var youtubeCmd *cobra.Command
	for _, cmd := range root.Commands() {
		if cmd.Name() == "youtube" {
			youtubeCmd = cmd
			break
		}
	}
	if youtubeCmd == nil {
		t.Fatal("youtube subcommand not found")
	}

	opts, err := globalOptionsFromCommand(youtubeCmd)
	if err != nil {
		t.Fatalf("globalOptionsFromCommand failed: %v", err)
	}
	if opts.configPath != "/tmp/custom.yaml" || opts.output != "." || opts.concurrency != 5 ||
		opts.rateLimit != "2M" || !opts.checksums || !opts.dryRun ||
		len(opts.allowExt) != 1 || opts.allowExt[0] != "cbz" || opts.maxAlbumItems != 5 ||
		opts.scrapeDelay != 250*time.Millisecond || opts.logFile != "/tmp/run.log" {
		t.Fatalf("explicit subcommand saw stale options: %+v", opts)
	}
}

func TestLoadGlobalConfigHonorsExplicitDefaultValues(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "output_dir: /from-config\nconcurrent_downloads: 9\nrate_limit: 1M\nchecksums: true\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadGlobalConfig(globalOptions{
		configPath:     configPath,
		output:         ".",
		outputSet:      true,
		concurrency:    5,
		concurrencySet: true,
		rateLimit:      "",
		rateLimitSet:   true,
		checksums:      false,
		checksumsSet:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutputDir != "." || cfg.ConcurrentDownloads != 5 || cfg.RateLimit != "" || cfg.Checksums {
		t.Fatalf("explicit defaults did not override config: %+v", cfg)
	}
}

func TestLoadGlobalConfigAllowExtFlag(t *testing.T) {
	cfg, err := loadGlobalConfig(globalOptions{
		configPath:  emptyTestConfig(t),
		allowExt:    []string{"cbz"},
		allowExtSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MediaExtensions) != 1 || cfg.MediaExtensions[0] != "cbz" {
		t.Fatalf("MediaExtensions = %v, want [cbz]", cfg.MediaExtensions)
	}
}

func TestApplyWatchDefaultsForcesNoTUI(t *testing.T) {
	got := applyWatchDefaults(globalOptions{watch: time.Minute})
	if !got.noTui {
		t.Fatal("watch mode must imply --no-tui")
	}
}

func TestWatchFlagParses(t *testing.T) {
	root := NewRootCmd()
	if err := root.PersistentFlags().Set("watch", "5m"); err != nil {
		t.Fatal(err)
	}
	opts, err := globalOptionsFromCommand(root)
	if err != nil {
		t.Fatal(err)
	}
	if opts.watch != 5*time.Minute {
		t.Fatalf("watch = %v, want 5m", opts.watch)
	}
}

func TestLoadGlobalConfigLogFileFlag(t *testing.T) {
	prev := logFile
	t.Cleanup(func() { logFile = prev })

	path := filepath.Join(t.TempDir(), "run.log")
	cfg, err := loadGlobalConfig(globalOptions{configPath: emptyTestConfig(t), logFile: path, logFileSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFile != path {
		t.Fatalf("LogFile = %q, want %q", cfg.LogFile, path)
	}
	if logFile == nil {
		t.Fatal("expected the log file to be opened")
	}
}

func TestLoadGlobalConfigScrapeDelayFlag(t *testing.T) {
	cfg, err := loadGlobalConfig(globalOptions{
		configPath:     emptyTestConfig(t),
		scrapeDelay:    750 * time.Millisecond,
		scrapeDelaySet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ScrapeDelay != 750*time.Millisecond {
		t.Fatalf("ScrapeDelay = %v, want 750ms", cfg.ScrapeDelay)
	}
}

func TestLoadGlobalConfigMaxAlbumItemsFlag(t *testing.T) {
	cfg, err := loadGlobalConfig(globalOptions{
		configPath:       emptyTestConfig(t),
		maxAlbumItems:    5,
		maxAlbumItemsSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxAlbumItems != 5 {
		t.Fatalf("MaxAlbumItems = %d, want 5", cfg.MaxAlbumItems)
	}
}

func TestLoadGlobalConfigChecksumsFlagEnables(t *testing.T) {
	cfg, err := loadGlobalConfig(globalOptions{
		configPath:   emptyTestConfig(t),
		checksums:    true,
		checksumsSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Checksums {
		t.Fatal("expected --checksums to enable checksum sidecars")
	}
}

func TestLoadGlobalConfigRejectsUnreasonableConcurrency(t *testing.T) {
	opts := globalOptions{
		configPath:     emptyTestConfig(t),
		concurrency:    2000000000,
		concurrencySet: true,
	}
	if _, err := loadGlobalConfig(opts); err == nil {
		t.Fatal("expected an error for --concurrent 2000000000")
	}

	opts.concurrency = 0
	opts.concurrencySet = true
	if _, err := loadGlobalConfig(opts); err == nil {
		t.Fatal("expected an error for --concurrent 0")
	}
}

func TestLoadGlobalConfigPerHostOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte("per_host_concurrency: 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{configPath: path})
	if err != nil || cfg.PerHostConcurrency != 4 {
		t.Fatalf("config per_host_concurrency = %v, %v; want 4", cfg, err)
	}
	cfg, err = loadGlobalConfig(globalOptions{configPath: path, perHost: 0, perHostSet: true})
	if err != nil || cfg.PerHostConcurrency != 0 {
		t.Fatalf("--per-host 0 (no limit) = %v, %v; want 0", cfg, err)
	}
	if _, err := loadGlobalConfig(globalOptions{configPath: path, perHost: -1, perHostSet: true}); err == nil {
		t.Fatal("expected an error for --per-host -1")
	}
}

func TestExpandHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"bare tilde expands to the home directory", "~", home, false},
		{"tilde slash expands under the home directory", "~/Downloads/foo", filepath.Join(home, "Downloads/foo"), false},
		{"tilde user is rejected", "~user/x", "", true},
		{"tilde user without a slash is rejected", "~user", "", true},
		{"relative path passes through unchanged", "relative/dir", "relative/dir", false},
		{"dot passes through unchanged", ".", ".", false},
		{"absolute path passes through unchanged", "/tmp/out", "/tmp/out", false},
		{"empty path passes through unchanged", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ExpandHomePath(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expandHomePath(%q) expected an error, got %q", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("expandHomePath(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("expandHomePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoadGlobalConfigExpandsHomeInOutputFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// An empty config file yields defaults, isolating the flag path.
	configPath := emptyTestConfig(t)

	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		output:     "~/out",
		outputSet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "out"); cfg.OutputDir != want {
		t.Errorf("OutputDir = %q, want %q (a quoted --output ~/out must not stay literal)", cfg.OutputDir, want)
	}
}

func TestLoadGlobalConfigRejectsTildeUserOutput(t *testing.T) {
	configPath := emptyTestConfig(t)

	_, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		output:     "~user/x",
		outputSet:  true,
	})
	if err == nil {
		t.Fatal("expected an error for a ~user output dir instead of a literal ~user directory")
	}
}

func TestLoadGlobalConfigReturnsMalformedYAMLError(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(configPath, []byte("output_dir: [\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGlobalConfig(globalOptions{configPath: configPath}); err == nil {
		t.Fatal("expected malformed config error")
	}
}

func TestAudioOnlyFlagBareValueDefaultsToM4A(t *testing.T) {
	root := NewRootCmd()
	if got := root.PersistentFlags().Lookup("audio-only").NoOptDefVal; got != "m4a" {
		t.Fatalf("audio-only NoOptDefVal = %q, want m4a (bare --audio-only must work)", got)
	}
}

func TestConcurrentFlagDefaultsTo3(t *testing.T) {
	root := NewRootCmd()
	if got := root.PersistentFlags().Lookup("concurrent").DefValue; got != "3" {
		t.Fatalf("concurrent default = %q, want 3", got)
	}
}

func TestLoadGlobalConfigProxyOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "proxy: http://config-proxy:3128\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		proxy:      "http://cli-proxy:8080",
		proxySet:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy != "http://cli-proxy:8080" {
		t.Errorf("expected CLI flag to override config, got %q", cfg.Proxy)
	}
}

func TestLoadGlobalConfigCookiesOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "cookies: " + filepath.Join(t.TempDir(), "config-cookies.txt") + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cliCookiesPath := filepath.Join(t.TempDir(), "cli-cookies.txt")
	cookieContent := ".example.com\tTRUE\t/\tFALSE\t9999999999\tsid\tcli_val\n"
	if err := os.WriteFile(cliCookiesPath, []byte(cookieContent), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		cookies:    cliCookiesPath,
		cookiesSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cookies != cliCookiesPath {
		t.Errorf("expected CLI to override cookies path, got %q", cfg.Cookies)
	}
	cookies := cfg.ParsedCookies()
	if len(cookies) != 1 || cookies[0].Name != "sid" {
		t.Errorf("expected parsed cookies from CLI path, got %v", cookies)
	}
}

func TestLoadGlobalConfigCookiesFileNotFound(t *testing.T) {
	_, err := loadGlobalConfig(globalOptions{
		cookies:    filepath.Join(t.TempDir(), "missing.txt"),
		cookiesSet: true,
	})
	if err == nil {
		t.Fatal("expected error for missing cookies file")
	}
}

func TestLoadGlobalConfigCookiesExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}
	cookieDir := filepath.Join(home, "scraper-test-cookie-home")
	if err := os.MkdirAll(cookieDir, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cookieDir)

	cookiesPath := filepath.Join(cookieDir, "c.txt")
	cookieContent := ".example.com\tTRUE\t/\tFALSE\t9999999999\tsession\tval\n"
	if err := os.WriteFile(cookiesPath, []byte(cookieContent), 0600); err != nil {
		t.Fatal(err)
	}
	tildePath := filepath.Join("~", "scraper-test-cookie-home", "c.txt")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfgContent := "cookies: " + tildePath + "\n"
	if err := os.WriteFile(configPath, []byte(cfgContent), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{configPath: configPath})
	if err != nil {
		t.Fatalf("expected tilde expansion to work: %v", err)
	}
	cookies := cfg.ParsedCookies()
	if len(cookies) != 1 || cookies[0].Name != "session" {
		t.Errorf("expected parsed cookies from tilde-path config, got %v", cookies)
	}
}

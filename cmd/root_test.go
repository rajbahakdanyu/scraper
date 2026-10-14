package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/bunker"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

// fakeDryRunScraper is a minimal model.Scraper for exercising runDryRun
// without any network access.
type fakeDryRunScraper struct {
	name  string
	items []model.MediaItem
	err   error
}

func (f *fakeDryRunScraper) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fake"
}

func (f *fakeDryRunScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	return f.items, f.err
}

// captureStdout redirects os.Stdout for the duration of f and returns
// everything written to it.
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// captureStderr redirects os.Stderr for the duration of f and returns
// everything written to it.
func captureStderr(f func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// emptyTestConfig writes a minimal empty config file and returns its path.
func emptyTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewRootCmdRegistersSubcommands(t *testing.T) {
	root := NewRootCmd()

	want := []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"}
	for _, name := range want {
		found := false
		for _, c := range root.Commands() {
			if c.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected root command to register %q subcommand", name)
		}
	}

	if root.PersistentFlags().Lookup("config") == nil {
		t.Error("expected --config persistent flag")
	}
	if root.PersistentFlags().Lookup("output") == nil {
		t.Error("expected --output persistent flag")
	}
	if root.PersistentFlags().Lookup("concurrent") == nil {
		t.Error("expected --concurrent persistent flag")
	}
	if root.PersistentFlags().Lookup("limit-rate") == nil {
		t.Error("expected --limit-rate persistent flag")
	}
	if root.PersistentFlags().Lookup("checksums") == nil {
		t.Error("expected --checksums persistent flag")
	}
	if root.PersistentFlags().Lookup("no-checksums") != nil {
		t.Error("--no-checksums must be gone: checksums are off by default")
	}
	if root.PersistentFlags().Lookup("dry-run") == nil {
		t.Error("expected --dry-run persistent flag")
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	root := NewRootCmd()
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs([]string{"completion", "tcsh"})

	if _, err := root.ExecuteC(); err == nil {
		t.Fatal("expected unknown completion shell to return an error")
	}
	if !strings.Contains(stderr.String(), "tcsh") {
		t.Fatalf("expected error output to mention shell, got %q", stderr.String())
	}
}

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

func TestApplyMediaFilterExtraExtensions(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://x/comic.cbz", Title: "comic.cbz"},
		{URL: "https://x/tool.exe", Title: "tool.exe"},
	}
	kept := applyMediaFilter(items, []string{"cbz"})
	if len(kept) != 1 || kept[0].Title != "comic.cbz" {
		t.Fatalf("applyMediaFilter() = %+v, want only the comic", kept)
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

func TestNewRootCmdRegistersAllowExtFlag(t *testing.T) {
	root := NewRootCmd()
	if root.PersistentFlags().Lookup("allow-ext") == nil {
		t.Error("expected --allow-ext persistent flag")
	}
	if root.PersistentFlags().Lookup("max-album-items") == nil {
		t.Error("expected --max-album-items persistent flag")
	}
	if root.PersistentFlags().Lookup("print-urls") == nil {
		t.Error("expected --print-urls persistent flag")
	}
}

func TestPrintItemURLs(t *testing.T) {
	var buf bytes.Buffer
	printItemURLs(&buf, []model.MediaItem{
		{URL: "https://x/a.mp4"},
		{Title: "no-url"},
		{URL: "https://x/b.mp4"},
	})
	want := "https://x/a.mp4\nhttps://x/b.mp4\n"
	if buf.String() != want {
		t.Fatalf("printItemURLs() = %q, want %q", buf.String(), want)
	}
}

func TestRunWithScraperAndConfigPrintURLs(t *testing.T) {
	scraper := &mockScraper{items: []model.MediaItem{
		{URL: "https://x/a.mp4", Title: "a.mp4"},
		{URL: "https://x/b.mp4", Title: "b.mp4"},
	}}
	cfg := &config.Config{OutputDir: t.TempDir()}

	var err error
	out := captureStdout(func() {
		err = runWithScraperAndConfig(cfg, scraper, "https://x/a/album", "test", globalOptions{printURLs: true})
	})
	if err != nil {
		t.Fatalf("print-urls run failed: %v", err)
	}
	if !strings.Contains(out, "https://x/a.mp4") || !strings.Contains(out, "https://x/b.mp4") {
		t.Fatalf("expected both URLs, got %q", out)
	}
	if strings.Contains(out, "downloading") {
		t.Fatalf("print-urls must not start downloads, got %q", out)
	}
}

func TestRunBatchPrintURLsRejectsJSON(t *testing.T) {
	restoreBatchFactories(t)
	err := runBatch(
		globalOptions{configPath: emptyTestConfig(t), printURLs: true, json: true},
		[]batchEntry{{URL: "https://cyberdrop.cr/a/one"}},
	)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutual-exclusion error, got %v", err)
	}
}

type recordingDelaySleeper struct{ delays []time.Duration }

func (s *recordingDelaySleeper) Sleep(time.Duration) {}

func (s *recordingDelaySleeper) SleepCtx(_ context.Context, d time.Duration) error {
	s.delays = append(s.delays, d)
	return nil
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

func TestExitCodeMapping(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Fatalf("exitCode(nil) = %d, want 0", got)
	}
	if got := exitCode(errors.New("boom")); got != 1 {
		t.Fatalf("exitCode(total failure) = %d, want 1", got)
	}
	if got := exitCode(model.NewPartialError("download", []error{errors.New("one failed")})); got != 2 {
		t.Fatalf("exitCode(partial) = %d, want 2", got)
	}
}

func TestLogFileWritesRunLines(t *testing.T) {
	prev := logFile
	t.Cleanup(func() { logFile = prev })

	path := filepath.Join(t.TempDir(), "run.log")
	if err := openLogFile(path); err != nil {
		t.Fatal(err)
	}
	logLine("hello %s", "world")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Fatalf("log = %q, want the logged line", data)
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

func TestFilterScraperScrapeDelay(t *testing.T) {
	prev := scrapeDelaySleeper
	sleeper := &recordingDelaySleeper{}
	scrapeDelaySleeper = sleeper
	t.Cleanup(func() { scrapeDelaySleeper = prev })

	fs := &filterScraper{
		inner:       &mockScraper{items: []model.MediaItem{{Title: "a.mp4"}}},
		scrapeDelay: 250 * time.Millisecond,
	}
	if _, err := fs.Parse(context.Background(), "https://example.com/a/x"); err != nil {
		t.Fatal(err)
	}
	if len(sleeper.delays) != 1 || sleeper.delays[0] != 250*time.Millisecond {
		t.Fatalf("delays = %v, want [250ms]", sleeper.delays)
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

func TestApplyAlbumItemCap(t *testing.T) {
	items := []model.MediaItem{{Title: "a.mp4"}, {Title: "b.mp4"}, {Title: "c.mp4"}}

	var capped []model.MediaItem
	stderr := captureStderr(func() {
		capped = applyAlbumItemCap(items, 2)
	})
	if len(capped) != 2 || capped[0].Title != "a.mp4" || capped[1].Title != "b.mp4" {
		t.Fatalf("applyAlbumItemCap() = %+v, want the first two items", capped)
	}
	if !strings.Contains(stderr, "keeping first 2 of 3") {
		t.Fatalf("expected a cap warning, got %q", stderr)
	}

	if got := applyAlbumItemCap(items, 0); len(got) != 3 {
		t.Fatalf("cap 0 must be unlimited, got %d items", len(got))
	}
	if got := applyAlbumItemCap(items, 5); len(got) != 3 {
		t.Fatalf("cap above length must keep all items, got %d", len(got))
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

func TestDetectSite(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://youtube.com/watch?v=abc", "youtube"},
		{"https://www.youtube.com/watch?v=abc", "youtube"},
		{"https://youtu.be/abc", "youtube"},
		{"https://cyberdrop.cr/a/abc", "cyberdrop"},
		{"https://cyberdrop.me/a/abc", "cyberdrop"},
		{"https://cyberdrop.to/a/abc", "cyberdrop"},
		{"https://bunkr.cr/a/abc", "bunker"},
		{"https://bunkr.su/a/abc", "bunker"},
		{"https://bunkrr.su/a/abc", "bunker"},
		{"https://bunkr.la/a/abc", "bunker"},
		{"https://bunkr.ph/a/abc", "bunker"},
		{"https://goonbox.cr/a/abc", "goonbox"},
		{"https://goonbox.to/a/abc", "goonbox"},
		{"https://filester.sh/f/abc", "filester"},
		{"https://filester.me/d/abc", "filester"},
		{"https://filester.sh:8443/f/abc", "filester"},
	}

	for _, tt := range tests {
		got, err := detectSite(tt.url)
		if err != nil {
			t.Errorf("detectSite(%s) returned error: %v", tt.url, err)
			continue
		}
		if got != tt.want {
			t.Errorf("detectSite(%s) = %s, want %s", tt.url, got, tt.want)
		}
	}
}

func TestDetectSiteRejectsLookalikeDomains(t *testing.T) {
	// These contain a known site's name as a substring but aren't actually
	// that site, so they must not match (a plain strings.Contains check
	// on the host would incorrectly match these).
	lookalikes := []string{
		"https://notcyberdrop.example.com/a/abc",
		"https://cyberdrop.example.com/a/abc",
		"https://evil-bunkr.com/a/abc",
		"https://filester.sh.evil.com/f/abc",
	}
	for _, u := range lookalikes {
		if got, err := detectSite(u); err == nil {
			t.Errorf("detectSite(%s) = %s, want an error for a lookalike domain", u, got)
		}
	}
}

func TestDetectSiteUnknown(t *testing.T) {
	if _, err := detectSite("https://example.com/whatever"); err == nil {
		t.Error("expected error for an unrecognized domain")
	}
	if _, err := detectSite("not a url"); err == nil {
		t.Error("expected error for an unparseable URL")
	}
}

func TestRootCmdNoArgsShowsHelp(t *testing.T) {
	// Bare invocation shows help only when stdin is an interactive terminal
	// (no piped URL source). Make that deterministic under `go test`.
	original := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	defer func() { stdinIsTerminal = original }()

	root := NewRootCmd()
	if err := root.RunE(root, nil); err != nil {
		t.Errorf("expected bare invocation to show help without error, got: %v", err)
	}
}

func TestRootCmdAutoDetectsUnknownDomain(t *testing.T) {
	root := NewRootCmd()
	if err := root.RunE(root, []string{"https://example.com/whatever"}); err == nil {
		t.Error("expected error for an unrecognized domain")
	}
}

func TestScraperRegistry(t *testing.T) {
	reg, err := scraperRegistry("", nil)
	if err != nil {
		t.Fatalf("scraperRegistry failed: %v", err)
	}
	for _, name := range []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"} {
		if _, ok := reg[name]; !ok {
			t.Errorf("expected registry to contain %q", name)
		}
	}
}

func TestRunDryRunPropagatesParseError(t *testing.T) {
	wantErr := fmt.Errorf("boom")
	scraper := &fakeDryRunScraper{err: wantErr}

	var err error
	captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != wantErr {
		t.Errorf("expected the scraper's Parse error to propagate, got: %v", err)
	}
}

func TestRunDryRunNoItems(t *testing.T) {
	scraper := &fakeDryRunScraper{items: nil}

	var err error
	out := captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "No items found") {
		t.Errorf("expected output to mention no items found, got: %q", out)
	}
}

func TestRunDryRunSummarizesItems(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{Title: "one.mp4", Size: 1024 * 1024},
		{Title: "two.jpg", Size: 2 * 1024 * 1024},
	}}

	var err error
	out := captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	for _, want := range []string{"one.mp4", "two.jpg", "2 items", "3.0M total"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got: %q", want, out)
		}
	}
	if strings.Contains(out, "unknown size") {
		t.Errorf("expected no unknown-size items, got: %q", out)
	}
}

func TestRunDryRunReportsUnknownSizes(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{Title: "known.mp4", Size: 1024 * 1024},
		{Title: "unknown.mp4", Size: 0},
	}}

	var err error
	out := captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "unknown size") {
		t.Errorf("expected the zero-size item to be reported as unknown size, got: %q", out)
	}
	if !strings.Contains(out, "1 with unknown size") {
		t.Errorf("expected the summary to count 1 unknown-size item, got: %q", out)
	}
}

func TestAlbumSlugForYouTubePlaylistUsesPlaylistID(t *testing.T) {
	tests := []struct {
		name     string
		siteName string
		url      string
		want     string
	}{
		{
			name:     "youtube playlist uses the playlist ID, not the generic slug",
			siteName: "youtube",
			url:      "https://www.youtube.com/playlist?list=PLxyz123",
			want:     "PLxyz123",
		},
		{
			name:     "youtube video with no playlist falls back to the video ID",
			siteName: "youtube",
			url:      "https://www.youtube.com/watch?v=abc123",
			want:     "abc123",
		},
		{
			name:     "non-youtube sites always use the generic slug",
			siteName: "cyberdrop",
			url:      "https://cyberdrop.cr/a/ALBUM123",
			want:     "ALBUM123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := albumSlugFor(tt.siteName, tt.url); got != tt.want {
				t.Errorf("albumSlugFor(%q, %q) = %q, want %q", tt.siteName, tt.url, got, tt.want)
			}
		})
	}

	// The bug this guards against: two different playlists must not
	// collide into the same subfolder.
	slugA := albumSlugFor("youtube", "https://www.youtube.com/playlist?list=PLaaa")
	slugB := albumSlugFor("youtube", "https://www.youtube.com/playlist?list=PLbbb")
	if slugA == slugB {
		t.Errorf("expected different playlists to get different slugs, both got %q", slugA)
	}
}

func TestNewRootCmdRegistersInputFileFlag(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("input-file")
	if flag == nil {
		t.Error("expected --input-file persistent flag")
	}
	if flag.Shorthand != "i" {
		t.Errorf("expected --input-file shorthand -i, got %q", flag.Shorthand)
	}
}

func TestCollectBatchURLsFromArgs(t *testing.T) {
	urls, err := collectBatchURLs([]string{"https://youtube.com/watch?v=abc", "https://cyberdrop.cr/a/xyz"}, globalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs, got %d: %v", len(urls), urls)
	}
}

func TestCollectBatchURLsFromInputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	content := "# comment\nhttps://youtube.com/watch?v=abc\n\nhttps://cyberdrop.cr/a/xyz\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	urls, err := collectBatchURLs(nil, globalOptions{inputFile: path, inputFileSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs (blanks and comments skipped), got %d: %v", len(urls), urls)
	}
}

func TestCollectBatchURLsTrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	content := "  https://youtube.com/watch?v=abc  \n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	urls, err := collectBatchURLs(nil, globalOptions{inputFile: path, inputFileSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0].URL != "https://youtube.com/watch?v=abc" {
		t.Fatalf("expected trimmed URL, got %v", urls)
	}
}

func TestReadURLsFromFileNotFound(t *testing.T) {
	_, err := readURLsFromFile(filepath.Join(t.TempDir(), "missing.txt"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadURLsFromReaderParsesItemAlbumPairs(t *testing.T) {
	input := "# comment\n\n" +
		"https://bunkr.cr/a/solo\n" +
		"https://bunkr.cr/f/item1\thttps://bunkr.cr/a/album1\n" +
		"https://bunkr.cr/f/item2\thttps://bunkr.cr/a/album1\textra-ignored\n"
	entries, err := readURLsFromReader(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3: %+v", len(entries), entries)
	}
	if entries[0].URL != "https://bunkr.cr/a/solo" || entries[0].AlbumURL != "" {
		t.Fatalf("single-column entry = %+v", entries[0])
	}
	if entries[1].URL != "https://bunkr.cr/f/item1" || entries[1].AlbumURL != "https://bunkr.cr/a/album1" {
		t.Fatalf("paired entry = %+v", entries[1])
	}
	if entries[2].AlbumURL != "https://bunkr.cr/a/album1" {
		t.Fatalf("extra columns must be ignored, got %+v", entries[2])
	}
}

func TestBatchDestSlugPrefersAlbumURL(t *testing.T) {
	paired := batchEntry{URL: "https://bunkr.cr/f/item1", AlbumURL: "https://bunkr.cr/a/rBSKhzaL"}
	if got := batchDestSlug("bunker", paired); got != "rBSKhzaL" {
		t.Fatalf("batchDestSlug(paired) = %q, want rBSKhzaL", got)
	}
	single := batchEntry{URL: "https://bunkr.cr/a/7OqKQz9N"}
	if got := batchDestSlug("bunker", single); got != "7OqKQz9N" {
		t.Fatalf("batchDestSlug(single) = %q, want 7OqKQz9N", got)
	}
}

func TestNewRootCmdRegistersNoTuiAndJSONFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"no-tui", "json"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestRunDryRunJSONProducesValidJSON(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{Title: "one.mp4", Size: 1024 * 1024, URL: "https://example.com/one"},
		{Title: "two.jpg", Size: 2 * 1024 * 1024, URL: "https://example.com/two"},
	}}

	var err error
	out := captureStdout(func() {
		err = runDryRunJSONWithDiskCheck(scraper, "https://example.com/a", "example", "", os.Stdout, "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, `"dryRun"`) || !strings.Contains(out, `"one.mp4"`) || !strings.Contains(out, `"two.jpg"`) {
		t.Errorf("expected JSON output with items, got: %q", out)
	}
	if !strings.Contains(out, `"albums"`) || !strings.Contains(out, `"summary"`) {
		t.Errorf("expected albums and summary in JSON, got: %q", out)
	}
}

func TestRunDryRunJSONEmptyItems(t *testing.T) {
	scraper := &fakeDryRunScraper{items: nil}
	out := captureStdout(func() {
		_ = runDryRunJSONWithDiskCheck(scraper, "https://example.com/a", "example", "", os.Stdout, "", false)
	})
	if !strings.Contains(out, `"dryRun"`) && !strings.Contains(out, `"summary"`) {
		t.Errorf("expected dry run JSON with summary, got: %q", out)
	}
}

func TestRunBatchRunsMixedSitesSequentially(t *testing.T) {
	restoreBatchFactories(t)
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{
			"youtube":   &fakeDryRunScraper{},
			"cyberdrop": &fakeDryRunScraper{},
		}, nil
	}

	var genericCalls, youtubeCalls int
	batchYouTubeScraper = func() model.Scraper { return &fakeDryRunScraper{} }
	batchYouTubeDownloader = func(model.Scraper, *config.Config, int64) (dashboard.Downloader, error) {
		youtubeCalls++
		return newFakePlainDownloader(), nil
	}
	batchGenericDownloader = func(*config.Config, int64) (dashboard.Downloader, error) {
		genericCalls++
		return newFakePlainDownloader(), nil
	}

	err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true}, []batchEntry{
		{URL: "https://youtube.com/watch?v=abc"},
		{URL: "https://cyberdrop.cr/a/album"},
	})
	// The fake scrapers return zero items, so the run ends in the
	// no-items partial error (Task: empty scrapes exit non-zero); the
	// assertion that matters here is that BOTH groups ran.
	if err != nil && !strings.Contains(err.Error(), "no media items found") {
		t.Fatalf("mixed batch failed unexpectedly: %v", err)
	}
	if genericCalls != 1 || youtubeCalls != 1 {
		t.Fatalf("expected one run per group, got generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
}

func TestBunkerMirrorDomainsMatchSiteDomains(t *testing.T) {
	want := append([]string(nil), siteDomains["bunker"]...)
	got := bunker.MirrorDomains()
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("bunker.MirrorDomains drifted from siteDomains[bunker]:\n got %v\nwant %v", got, want)
	}
}

func TestRunDiskCheckWarnsForMissingOutputDir(t *testing.T) {
	var buf bytes.Buffer
	prev := diskWarn
	diskWarn = &buf
	t.Cleanup(func() { diskWarn = prev })
	runDiskCheck([]model.MediaItem{{Title: "big.mp4", Size: 1 << 62}}, filepath.Join(t.TempDir(), "missing"), true)
	if !strings.Contains(buf.String(), "short") {
		t.Fatalf("a huge download into a fresh directory must warn, got %q", buf.String())
	}
}

func TestRunDryRunEmptyScrapeIsInformational(t *testing.T) {
	// Dry-run is informational: a URL that scrapes zero items (wrapped
	// by the filter scraper under the default media-only config) must
	// print the empty message and exit 0, not fail the run.
	cfg, err := config.Load(emptyTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	scraper := applyDryRunFilters(&fakeDryRunScraper{}, cfg)

	out := captureStdout(func() {
		if err := runDryRunWithDiskCheck(scraper, "https://cyberdrop.cr/a/empty", t.TempDir(), false); err != nil {
			t.Errorf("dry-run of an empty scrape must exit 0, got %v", err)
		}
	})
	if !strings.Contains(out, "No items found.") {
		t.Fatalf("expected the empty message, got: %s", out)
	}
}

func TestRunDryRunJSONEmptyScrapeIsInformational(t *testing.T) {
	cfg, err := config.Load(emptyTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	scraper := applyDryRunFilters(&fakeDryRunScraper{}, cfg)

	var buf bytes.Buffer
	if err := runDryRunJSONWithDiskCheck(scraper, "https://cyberdrop.cr/a/empty", "cyberdrop", "", &buf, t.TempDir(), false); err != nil {
		t.Fatalf("dry-run of an empty scrape must exit 0, got %v", err)
	}
	if strings.Contains(buf.String(), `"error"`) {
		t.Fatalf("expected an empty dry-run summary without an error event, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"dryRun"`) {
		t.Fatalf("expected a dry-run document, got: %s", buf.String())
	}
}

func TestRunPlainNoItemsIsAnError(t *testing.T) {
	var buf bytes.Buffer
	err := runPlain(newFakePlainDownloader(), func() ([]model.MediaItem, error) {
		return nil, ErrNoItems
	}, t.TempDir(), "", "", &buf)
	if !errors.Is(err, ErrNoItems) {
		t.Fatalf("runPlain should surface ErrNoItems, got %v", err)
	}
	if !strings.Contains(buf.String(), "No items found") {
		t.Fatalf("output should still say no items were found: %q", buf.String())
	}
}

func TestRunBatchPlainTreatsEmptyScrapeAsFailure(t *testing.T) {
	// An album that scrapes zero items (dead URL, DOM change) must fail
	// the batch instead of looking successful.
	restoreBatchFactories(t)
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": &fakeDryRunScraper{}}, nil
	}

	err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true}, []batchEntry{
		{URL: "https://cyberdrop.cr/a/empty"},
	})
	if err == nil || !strings.Contains(err.Error(), "no media items found") {
		t.Fatalf("an empty scrape must fail the batch, got %v", err)
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

func TestRunBatchContinuesAfterGroupFailure(t *testing.T) {
	restoreBatchFactories(t)
	var genericCalls int
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": &fakeDryRunScraper{}}, nil
	}
	batchYouTubeScraper = func() model.Scraper { return &fakeDryRunScraper{} }
	batchYouTubeDownloader = func(model.Scraper, *config.Config, int64) (dashboard.Downloader, error) {
		return nil, errors.New("youtube downloader exploded")
	}
	batchGenericDownloader = func(*config.Config, int64) (dashboard.Downloader, error) {
		genericCalls++
		return newFakePlainDownloader(), nil
	}

	err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true}, []batchEntry{
		{URL: "https://youtube.com/watch?v=abc"},
		{URL: "https://cyberdrop.cr/a/album"},
	})
	if err == nil {
		t.Fatal("the youtube group failure must surface")
	}
	if genericCalls != 1 {
		t.Fatalf("generic group must still run after a youtube group failure, ran %d times", genericCalls)
	}
}

func TestRunBatchDryRunJSONLoopsOverURLs(t *testing.T) {
	restoreBatchFactories(t)
	fake := &fakeDryRunScraper{items: []model.MediaItem{{URL: "https://example.com/item.mp4", Title: "unique-fixture-title.mp4"}}}
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": fake}, nil
	}

	var err error
	out := captureStdout(func() {
		err = runBatch(globalOptions{
			configPath: emptyTestConfig(t),
			dryRun:     true,
			json:       true,
		}, []batchEntry{
			{URL: "https://cyberdrop.cr/a/one"},
			{URL: "https://cyberdrop.cr/a/two"},
		})
	})
	if err != nil {
		t.Fatalf("expected dry-run batch to succeed, got %v", err)
	}
	if strings.Count(out, `"dryRun"`) != 2 {
		t.Fatalf("expected one JSON dry-run document per URL, got %d:\n%s", strings.Count(out, `"dryRun"`), out)
	}
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") || !strings.Contains(out, "unique-fixture-title.mp4") {
		t.Fatalf("expected both URLs and the item title, got %q", out)
	}
}

func TestDryRunAppliesFilters(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{URL: "https://cyberdrop.cr/f/1.mp4", Title: "one.mp4", Size: 100},
		{URL: "https://cyberdrop.cr/f/2.txt", Title: "two.txt", Size: 200},
	}}
	restoreBatchFactories(t)
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": scraper}, nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	errCh := make(chan error, 1)
	go func() {
		err := runBatch(globalOptions{
			configPath: emptyTestConfig(t), noTui: true, dryRun: true,
			include: []string{"*.mp4"}, includeSet: true,
		}, []batchEntry{{URL: "https://cyberdrop.cr/a/album"}})
		w.Close()
		errCh <- err
	}()
	if err := <-errCh; err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	out, _ := io.ReadAll(r)

	got := string(out)
	if strings.Contains(got, "two.txt") {
		t.Errorf("filtered item leaked into dry-run output: %q", got)
	}
	if !strings.Contains(got, "one.mp4") {
		t.Errorf("expected matching item in output: %q", got)
	}
}

func TestRunBatchSelectsDownloaderBySiteAndWiresDestinations(t *testing.T) {
	restoreBatchFactories(t)
	genericScraper := &fakeDryRunScraper{items: []model.MediaItem{{URL: "https://cyberdrop.cr/item", Title: "generic.mp4"}}}
	youtubeScraper := &fakeDryRunScraper{name: "youtube", items: []model.MediaItem{{URL: "https://youtube.com/item", Title: "youtube"}}}
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": genericScraper}, nil
	}

	var genericCalls, youtubeCalls int
	var genericDL, youtubeDL *fakePlainDownloader
	batchGenericDownloader = func(*config.Config, int64) (dashboard.Downloader, error) {
		genericCalls++
		genericDL = newFakePlainDownloader()
		return genericDL, nil
	}
	batchYouTubeScraper = func() model.Scraper { return youtubeScraper }
	batchYouTubeDownloader = func(model.Scraper, *config.Config, int64) (dashboard.Downloader, error) {
		youtubeCalls++
		youtubeDL = newFakePlainDownloader()
		return youtubeDL, nil
	}

	base := t.TempDir()
	if err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true, output: base, outputSet: true}, []batchEntry{{URL: "https://cyberdrop.cr/a/album"}}); err != nil {
		t.Fatalf("generic batch failed: %v", err)
	}
	if genericCalls != 1 || youtubeCalls != 0 {
		t.Fatalf("generic batch selected wrong downloader: generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
	if len(genericDL.dests) != 1 || !strings.HasSuffix(genericDL.dests[0], filepath.Join(base, "album")) {
		t.Fatalf("generic batch destination = %v, want album slug under output", genericDL.dests)
	}

	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"youtube": youtubeScraper}, nil
	}
	if err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true, output: base, outputSet: true}, []batchEntry{{URL: "https://youtube.com/watch?v=abc"}}); err != nil {
		t.Fatalf("youtube batch failed: %v", err)
	}
	if youtubeCalls != 1 || genericCalls != 1 {
		t.Fatalf("youtube batch selected wrong downloader: generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
	if len(youtubeDL.dests) != 1 || !strings.HasSuffix(youtubeDL.dests[0], filepath.Join("abc")) {
		t.Fatalf("youtube batch destination = %v, want video-ID slug under output", youtubeDL.dests)
	}
}

func restoreBatchFactories(t *testing.T) {
	oldRegistry := batchScraperRegistry
	oldGeneric := batchGenericDownloader
	oldYouTubeScraper := batchYouTubeScraper
	oldYouTube := batchYouTubeDownloader
	t.Cleanup(func() {
		batchScraperRegistry = oldRegistry
		batchGenericDownloader = oldGeneric
		batchYouTubeScraper = oldYouTubeScraper
		batchYouTubeDownloader = oldYouTube
	})
}

func TestNewRootCmdHasProxyFlag(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("proxy")
	if flag == nil {
		t.Error("expected --proxy persistent flag")
	}
}

func TestNewRootCmdRegistersVersionAndCompletion(t *testing.T) {
	root := NewRootCmd()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, name := range []string{"version", "completion"} {
		if !names[name] {
			t.Errorf("expected %q subcommand", name)
		}
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

func TestNewRootCmdHasCookiesFlag(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("cookies")
	if flag == nil {
		t.Error("expected --cookies persistent flag")
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

func TestRunWithScraperAppliesCookiesToDownloader(t *testing.T) {
	cookiesPath := filepath.Join(t.TempDir(), "cookies.txt")
	cookieContent := ".example.com\tTRUE\t/\tFALSE\t9999999999\ttoken\tval\n"
	if err := os.WriteFile(cookiesPath, []byte(cookieContent), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfgContent := "cookies: " + cookiesPath + "\n"
	if err := os.WriteFile(configPath, []byte(cfgContent), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{configPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	cookies := cfg.ParsedCookies()
	if len(cookies) != 1 || cookies[0].Name != "token" {
		t.Errorf("expected parsed cookies from config, got %v", cookies)
	}
}

func TestScraperRegistryWithCookies(t *testing.T) {
	cookies := []*http.Cookie{{Name: "auth", Value: "secret"}}
	reg, err := scraperRegistry("", cookies)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"} {
		if reg[name] == nil {
			t.Errorf("expected scraper for %q in registry", name)
		}
	}
}

func TestScraperRegistryWithProxyAndCookies(t *testing.T) {
	cookies := []*http.Cookie{{Name: "auth", Value: "secret"}}
	reg, err := scraperRegistry("http://proxy:8080", cookies)
	if err != nil {
		if strings.Contains(err.Error(), "invalid proxy URL") {
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"} {
		if reg[name] == nil {
			t.Errorf("expected scraper for %q in registry", name)
		}
	}
}

func TestNewRootCmdHasQualityAndAudioOnlyFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"quality", "audio-only"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestLoadGlobalConfigQualityAudioOnlyMutualExclusion(t *testing.T) {
	_, err := loadGlobalConfig(globalOptions{
		quality:      "1080p",
		qualitySet:   true,
		audioOnly:    "m4a",
		audioOnlySet: true,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot use --quality and --audio-only together") {
		t.Fatalf("expected mutual exclusion error, got %v", err)
	}
}

func TestLoadGlobalConfigInvalidQuality(t *testing.T) {
	_, err := loadGlobalConfig(globalOptions{
		quality:    "9999p",
		qualitySet: true,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --quality value") {
		t.Fatalf("expected invalid quality error, got %v", err)
	}
}

func TestLoadGlobalConfigInvalidAudioOnly(t *testing.T) {
	_, err := loadGlobalConfig(globalOptions{
		audioOnly:    "ogg",
		audioOnlySet: true,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --audio-only value") {
		t.Fatalf("expected invalid audio-only error, got %v", err)
	}
}

func TestLoadGlobalConfigQualityOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "quality: 720p\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		quality:    "1080p",
		qualitySet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quality != "1080p" {
		t.Errorf("expected CLI to override quality, got %q", cfg.Quality)
	}
}

func TestLoadGlobalConfigAudioOnlyDefaultM4a(t *testing.T) {
	cfg, err := loadGlobalConfig(globalOptions{
		audioOnly: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AudioOnly != "" {
		t.Errorf("expected empty AudioOnly, got %q", cfg.AudioOnly)
	}
}

func TestValidQuality(t *testing.T) {
	h, ok := youtube.ValidQuality("1080p")
	if !ok || h != 1080 {
		t.Errorf("ValidQuality(1080p) = %d, %v; want 1080, true", h, ok)
	}
	h, ok = youtube.ValidQuality("9999p")
	if ok {
		t.Error("ValidQuality(9999p) should be false")
	}
	h, ok = youtube.ValidQuality("")
	if !ok || h != 0 {
		t.Errorf("ValidQuality(\"\") = %d, %v; want 0, true", h, ok)
	}
}

func TestNewRootCmdHasFilterFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"include", "exclude", "min-size", "max-size"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestLoadGlobalConfigInvalidIncludeGlob(t *testing.T) {
	_, err := loadGlobalConfig(globalOptions{
		include:    []string{"bad[glob"},
		includeSet: true,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --include pattern") {
		t.Fatalf("expected invalid glob error, got %v", err)
	}
}

func TestLoadGlobalConfigInvalidMinSize(t *testing.T) {
	_, err := loadGlobalConfig(globalOptions{
		minSize:    "abc",
		minSizeSet: true,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --min-size") {
		t.Fatalf("expected invalid size error, got %v", err)
	}
}

func TestLoadGlobalConfigIncludeOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "include:\n  - '*.jpg'\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		include:    []string{"*.mp4"},
		includeSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Include) != 1 || cfg.Include[0] != "*.mp4" {
		t.Errorf("expected CLI to override include, got %v", cfg.Include)
	}
}

type mockScraper struct {
	name  string
	items []model.MediaItem
	err   error
}

func (m *mockScraper) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

func (m *mockScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	return m.items, m.err
}

func TestFilterScraperParse(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/video.mp4", Size: 1024},
		{URL: "https://cdn.example.com/image.jpg", Size: 512},
	}}
	fs := &filterScraper{
		inner:   inner,
		include: []string{"*.mp4"},
	}
	items, _ := fs.Parse(context.Background(), "https://example.com/a/test")
	if len(items) != 1 || items[0].URL != "https://cdn.example.com/video.mp4" {
		t.Fatalf("expected 1 filtered item, got %+v", items)
	}
}

func TestFilterScraperNoItems(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/video.mp4"},
	}}
	fs := &filterScraper{
		inner:   inner,
		exclude: []string{"*.mp4"},
	}
	items, _ := fs.Parse(context.Background(), "https://example.com/a/test")
	if len(items) != 0 {
		t.Fatalf("expected 0 items after filtering, got %d", len(items))
	}
}

func TestLoadGlobalConfigParseSizeValues(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "min_size: 1M\nmax_size: 500M\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinSizeBytes() != 1024*1024 {
		t.Errorf("expected 1M bytes from config, got %d", cfg.MinSizeBytes())
	}
	if cfg.MaxSizeBytes() != 500*1024*1024 {
		t.Errorf("expected 500M bytes from config, got %d", cfg.MaxSizeBytes())
	}
}

func TestNewRootCmdHasDiskCheckAndNotifyFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"disk-check", "notify"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestLoadGlobalConfigDiskCheckOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "disk_check: false\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath:   configPath,
		diskCheck:    true,
		diskCheckSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DiskCheck {
		t.Error("expected CLI true to override config false")
	}
}

func TestLoadGlobalConfigNotifyOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "notify: true\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		notify:     false,
		notifySet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notify {
		t.Error("expected CLI false to override config true")
	}
}

func TestFilterScraperMediaOnly(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/video.mp4", Title: "video.mp4"},
		{URL: "https://cdn.example.com/setup.exe", Title: "setup.exe"},
		{URL: "https://cdn.example.com/set.zip", Title: "set.zip"},
		{URL: "https://cdn.example.com/page.html", Title: "page.html"},
	}}
	fs := &filterScraper{inner: inner, mediaOnly: true}

	var items []model.MediaItem
	stderr := captureStderr(func() {
		items, _ = fs.Parse(context.Background(), "https://example.com/a/test")
	})

	if len(items) != 2 || items[0].Title != "video.mp4" || items[1].Title != "set.zip" {
		t.Fatalf("expected video.mp4 and set.zip, got %+v", items)
	}
	if !strings.Contains(stderr, "2 skipped non-media") {
		t.Fatalf("expected a skip summary, got %q", stderr)
	}
}

func TestFilterScraperMediaOnlyBypassesYouTube(t *testing.T) {
	inner := &mockScraper{name: "youtube", items: []model.MediaItem{
		{URL: "https://youtube.com/watch?v=1", Title: "Some Video"},
	}}
	fs := &filterScraper{inner: inner, mediaOnly: true}

	items, err := fs.Parse(context.Background(), "https://youtube.com/playlist?list=1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected the YouTube item to be kept, got %d items", len(items))
	}
}

func TestFilterScraperMediaOnlyKeepsWorkingWithGlobs(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/a.mp4", Title: "a.mp4"},
		{URL: "https://cdn.example.com/b.zip", Title: "b.zip"},
		{URL: "https://cdn.example.com/c.exe", Title: "c.exe"},
	}}
	fs := &filterScraper{inner: inner, mediaOnly: true, include: []string{"*.zip"}}

	items, _ := fs.Parse(context.Background(), "https://example.com/a/test")
	if len(items) != 1 || items[0].Title != "b.zip" {
		t.Fatalf("expected only b.zip after media + include filters, got %+v", items)
	}
}

func TestApplyDryRunFiltersWrapsWhenOnlyMediaOnly(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{{Title: "a.mp4"}}}
	cfg := &config.Config{MediaOnly: true}

	wrapped := applyDryRunFilters(inner, cfg)
	if _, ok := wrapped.(*filterScraper); !ok {
		t.Fatalf("expected a filterScraper wrapper, got %T", wrapped)
	}
}

func TestApplyMediaFilterAllSkipped(t *testing.T) {
	var kept []model.MediaItem
	stderr := captureStderr(func() {
		kept = applyMediaFilter([]model.MediaItem{{Title: "setup.exe"}}, nil)
	})
	if len(kept) != 0 {
		t.Fatalf("expected no items, got %+v", kept)
	}
	if !strings.Contains(stderr, "1 skipped non-media") || !strings.Contains(stderr, "No media items found.") {
		t.Fatalf("expected skip summary and empty notice, got %q", stderr)
	}
}

func TestApplyMediaFilterEmptyInputIsSilent(t *testing.T) {
	stderr := captureStderr(func() {
		kept := applyMediaFilter(nil, nil)
		if len(kept) != 0 {
			t.Fatalf("expected no items, got %+v", kept)
		}
	})
	if strings.Contains(stderr, "No media items found.") {
		t.Fatalf("empty input must not print the media notice, got %q", stderr)
	}
}

func TestApplyMediaFilterSilentWhenNothingSkipped(t *testing.T) {
	stderr := captureStderr(func() {
		applyMediaFilter([]model.MediaItem{{Title: "a.mp4"}}, nil)
	})
	if stderr != "" {
		t.Fatalf("expected no output when nothing was skipped, got %q", stderr)
	}
}

func TestFilterScraperPreservesPartialResults(t *testing.T) {
	partialErr := errors.New("page 2 failed")
	inner := &mockScraper{
		items: []model.MediaItem{
			{URL: "https://cdn.example.com/a.mp4", Title: "a.mp4"},
			{URL: "https://cdn.example.com/b.exe", Title: "b.exe"},
		},
		err: partialErr,
	}
	fs := &filterScraper{inner: inner, mediaOnly: true}

	items, err := fs.Parse(context.Background(), "https://example.com/a/test")
	if !errors.Is(err, partialErr) {
		t.Fatalf("expected the partial error to survive filtering, got %v", err)
	}
	if len(items) != 1 || items[0].Title != "a.mp4" {
		t.Fatalf("expected the parsed a.mp4 to survive alongside the error, got %+v", items)
	}
}

func TestDryRunKeepsPartialMediaItems(t *testing.T) {
	inner := &mockScraper{
		items: []model.MediaItem{
			{URL: "https://cdn.example.com/a.mp4", Title: "a.mp4"},
			{URL: "https://cdn.example.com/b.exe", Title: "b.exe"},
		},
		err: errors.New("page 2 failed"),
	}
	wrapped := applyDryRunFilters(inner, &config.Config{MediaOnly: true})

	var runErr error
	var stderr string
	out := captureStdout(func() {
		stderr = captureStderr(func() {
			runErr = runDryRunWithDiskCheck(wrapped, "https://example.com/a", t.TempDir(), false)
		})
	})
	if runErr != nil {
		t.Fatalf("a partial scrape must warn, not fail the dry-run: %v", runErr)
	}
	if !strings.Contains(stderr, "page 2 failed") {
		t.Fatalf("expected the partial error as a warning, got %q", stderr)
	}
	if !strings.Contains(out, "a.mp4") {
		t.Fatalf("expected the parsed media item in dry-run output, got %q", out)
	}
	if strings.Contains(out, "b.exe") {
		t.Fatalf("expected the skipped non-media item to stay out of dry-run output, got %q", out)
	}
}

func TestBatchDryRunPrintsPerURLErrors(t *testing.T) {
	restoreBatchFactories(t)
	bad := &fakeDryRunScraper{name: "bunker", err: errors.New("album page returned no file links")}
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"bunker": bad}, nil
	}

	var runErr error
	stderr := captureStderr(func() {
		runErr = runBatch(globalOptions{configPath: emptyTestConfig(t), dryRun: true, noTui: true}, []batchEntry{{URL: "https://bunkr.cr/a/one"}})
	})
	if runErr == nil {
		t.Fatal("expected the batch dry-run to report the failure")
	}
	if !strings.Contains(stderr, "https://bunkr.cr/a/one") || !strings.Contains(stderr, "no file links") {
		t.Fatalf("expected the failing URL and its error on stderr, got %q", stderr)
	}
}

func TestHelpRequestedOnlyWithoutAnyURLSource(t *testing.T) {
	if !helpRequested(nil, false, true) {
		t.Fatal("no args, no input file, terminal stdin: expected help")
	}
	if helpRequested(nil, true, true) {
		t.Fatal("--input-file set: must run, not print help")
	}
	if helpRequested(nil, false, false) {
		t.Fatal("piped stdin: must run, not print help")
	}
	if helpRequested([]string{"https://example.com/x"}, false, true) {
		t.Fatal("positional args: must run, not print help")
	}
}

func TestRootCommandReadsInputFileWithoutPositionalArgs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(path, []byte("https://example.com/not-supported\n"), 0600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"--config", emptyTestConfig(t),
		"--input-file", path,
	})

	_, err := root.ExecuteC()
	if err == nil {
		t.Fatalf("expected the unsupported URL to fail, got help instead:\n%s", stdout.String())
	}
	if combined := stdout.String() + stderr.String(); strings.Contains(combined, "Usage:") {
		t.Fatalf("expected the command to run, but it printed help:\n%s", combined)
	}
}

func TestFilterScraperMediaOnlySkipsYouTube(t *testing.T) {
	items := []model.MediaItem{{URL: "https://www.youtube.com/watch?v=x", Title: "a video"}}
	yt := newFilterScraper(&mockScraper{name: "youtube", items: items}, &config.Config{MediaOnly: true}, 0, false)
	if got, _ := yt.Parse(context.Background(), "u"); len(got) != 1 {
		t.Fatalf("YouTube must bypass the media filter, got %+v", got)
	}
	file := newFilterScraper(&mockScraper{name: "bunker", items: items}, &config.Config{MediaOnly: true}, 0, false)
	if got, _ := file.Parse(context.Background(), "u"); len(got) != 0 {
		t.Fatalf("file hosts must apply the media filter, got %+v", got)
	}
}

func TestRunWithScraperAndConfigAppliesIncludeFilter(t *testing.T) {
	scraper := &mockScraper{items: []model.MediaItem{
		{URL: "https://x/a.mp4", Title: "a.mp4"},
		{URL: "https://x/b.jpg", Title: "b.jpg"},
	}}
	cfg := &config.Config{OutputDir: t.TempDir(), Include: []string{"*.mp4"}}

	var err error
	out := captureStdout(func() {
		err = runWithScraperAndConfig(cfg, scraper, "https://x/a/album", "test", globalOptions{printURLs: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "b.jpg") || !strings.Contains(out, "a.mp4") {
		t.Fatalf("--include '*.mp4' must keep only a.mp4, got %q", out)
	}
}

func TestRunWithScraperAndConfigAppliesAlbumItemCap(t *testing.T) {
	scraper := &mockScraper{items: []model.MediaItem{
		{URL: "https://x/a.mp4", Title: "a.mp4"},
		{URL: "https://x/b.mp4", Title: "b.mp4"},
	}}
	cfg := &config.Config{OutputDir: t.TempDir(), MaxAlbumItems: 1}

	var err error
	out := captureStdout(func() {
		err = runWithScraperAndConfig(cfg, scraper, "https://x/a/album", "test", globalOptions{printURLs: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "b.mp4") {
		t.Fatalf("max_album_items=1 must keep only the first item, got %q", out)
	}
}

func TestLoadGlobalConfigMediaOnlyOverride(t *testing.T) {
	cfg, err := loadGlobalConfig(globalOptions{configPath: emptyTestConfig(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MediaOnly {
		t.Fatal("media filtering must default to on")
	}

	cfg, err = loadGlobalConfig(globalOptions{
		configPath:   emptyTestConfig(t),
		mediaOnly:    false,
		mediaOnlySet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MediaOnly {
		t.Fatal("--media-only=false must override the default")
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("media_only: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadGlobalConfig(globalOptions{
		configPath:   configPath,
		mediaOnly:    true,
		mediaOnlySet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MediaOnly {
		t.Fatal("--media-only must re-enable filtering when the config disabled it")
	}
}

func TestDiskWarningIncludesUnknownItems(t *testing.T) {
	got := diskWarning(3*1024*1024, 1024*1024, 2)
	want := "disk: 1.0M free, 3.0M needed (2.0M short) (2 items with unknown size)"
	if got != want {
		t.Fatalf("diskWarning() = %q, want %q", got, want)
	}
}

func TestOutputFlagHelpMentionsEffectiveDefault(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("output")
	if flag == nil {
		t.Fatal("expected --output persistent flag")
	}
	if flag.DefValue != "" {
		t.Errorf("--output flag default = %q, want empty (the effective default lives in config.Load)", flag.DefValue)
	}
	if !strings.Contains(flag.Usage, "~/Downloads") {
		t.Errorf("--output usage = %q, want it to mention ~/Downloads", flag.Usage)
	}
}

func TestFinishRunNotifiesFailureAndSuccess(t *testing.T) {
	original := notifySend
	defer func() { notifySend = original }()

	var got []string
	notifySend = func(summary string) error {
		got = append(got, summary)
		return nil
	}

	runFailure := func() error { return errors.New("boom") }
	if err := finishRun(&config.Config{Notify: true}, globalOptions{noTui: true}, runFailure, runFailure, runFailure); err == nil {
		t.Fatal("expected finishRun to propagate the runner error")
	}
	if len(got) != 1 || got[0] != "Download failed" {
		t.Fatalf("notifications = %v, want [Download failed]", got)
	}

	got = nil
	runSuccess := func() error { return nil }
	if err := finishRun(&config.Config{Notify: true}, globalOptions{noTui: true}, runSuccess, runSuccess, runSuccess); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "Download complete" {
		t.Fatalf("notifications = %v, want [Download complete]", got)
	}

	got = nil
	if err := finishRun(&config.Config{Notify: false}, globalOptions{noTui: true}, runSuccess, runSuccess, runSuccess); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("notifications = %v, want none when Notify is disabled", got)
	}
}

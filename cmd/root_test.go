package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

// fakeDryRunScraper is a minimal model.Scraper for exercising runDryRun
// without any network access.
type fakeDryRunScraper struct {
	items []model.MediaItem
	err   error
}

func (f *fakeDryRunScraper) Name() string { return "fake" }

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
	if root.PersistentFlags().Lookup("no-checksums") == nil {
		t.Error("expected --no-checksums persistent flag")
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
	if err := root.PersistentFlags().Set("no-checksums", "true"); err != nil {
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
		opts.rateLimit != "2M" || !opts.skipChecksums || !opts.dryRun {
		t.Fatalf("explicit subcommand saw stale options: %+v", opts)
	}
}

func TestLoadGlobalConfigHonorsExplicitDefaultValues(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "output_dir: /from-config\nconcurrent_downloads: 9\nrate_limit: 1M\nskip_checksums: true\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadGlobalConfig(globalOptions{
		configPath:       configPath,
		output:           ".",
		outputSet:        true,
		concurrency:      5,
		concurrencySet:   true,
		rateLimit:        "",
		rateLimitSet:     true,
		skipChecksums:    false,
		skipChecksumsSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutputDir != "." || cfg.ConcurrentDownloads != 5 || cfg.RateLimit != "" || cfg.SkipChecksums {
		t.Fatalf("explicit defaults did not override config: %+v", cfg)
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
			got, err := expandHomePath(tt.in)
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
	// A nonexistent config path yields defaults, isolating the flag path.
	configPath := filepath.Join(t.TempDir(), "nonexistent.yaml")

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
	configPath := filepath.Join(t.TempDir(), "nonexistent.yaml")

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
		err = runDryRun(scraper, "https://example.com/a")
	})
	if err != wantErr {
		t.Errorf("expected the scraper's Parse error to propagate, got: %v", err)
	}
}

func TestRunDryRunNoItems(t *testing.T) {
	scraper := &fakeDryRunScraper{items: nil}

	var err error
	out := captureStdout(func() {
		err = runDryRun(scraper, "https://example.com/a")
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
		err = runDryRun(scraper, "https://example.com/a")
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
		err = runDryRun(scraper, "https://example.com/a")
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
			name:     "youtube video with no playlist falls back to the generic slug",
			siteName: "youtube",
			url:      "https://www.youtube.com/watch?v=abc123",
			want:     "watch",
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
	if len(urls) != 1 || urls[0] != "https://youtube.com/watch?v=abc" {
		t.Fatalf("expected trimmed URL, got %v", urls)
	}
}

func TestReadURLsFromFileNotFound(t *testing.T) {
	_, err := readURLsFromFile(filepath.Join(t.TempDir(), "missing.txt"))
	if err == nil {
		t.Fatal("expected error for missing file")
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
		err = runDryRunJSON(scraper, "https://example.com/a", "example", os.Stdout)
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
		_ = runDryRunJSON(scraper, "https://example.com/a", "example", os.Stdout)
	})
	if !strings.Contains(out, `"dryRun"`) && !strings.Contains(out, `"summary"`) {
		t.Errorf("expected dry run JSON with summary, got: %q", out)
	}
}

func TestRunBatchRejectsMixedYouTubeAndGenericSites(t *testing.T) {
	restoreBatchFactories(t)
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{
			"youtube":   &fakeDryRunScraper{},
			"cyberdrop": &fakeDryRunScraper{},
		}, nil
	}

	err := runBatch(globalOptions{configPath: filepath.Join(t.TempDir(), "missing.yaml")}, []string{
		"https://youtube.com/watch?v=abc",
		"https://cyberdrop.cr/a/album",
	})
	if err == nil || !strings.Contains(err.Error(), "same site type") {
		t.Fatalf("expected mixed-site rejection, got %v", err)
	}
}

func TestRunBatchDryRunJSONLoopsOverURLs(t *testing.T) {
	restoreBatchFactories(t)
	fake := &fakeDryRunScraper{items: []model.MediaItem{{URL: "https://example.com/item", Title: "item"}}}
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": fake}, nil
	}

	var err error
	out := captureStdout(func() {
		err = runBatch(globalOptions{
			configPath: filepath.Join(t.TempDir(), "missing.yaml"),
			dryRun:     true,
			json:       true,
		}, []string{
			"https://cyberdrop.cr/a/one",
			"https://cyberdrop.cr/a/two",
		})
	})
	if err != nil {
		t.Fatalf("expected dry-run batch to succeed, got %v", err)
	}
	if strings.Count(out, `"dryRun"`) != 2 {
		t.Fatalf("expected one JSON dry-run document per URL, got %d:\n%s", strings.Count(out, `"dryRun"`), out)
	}
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") || !strings.Contains(out, "item") {
		t.Fatalf("expected both URLs and item output, got %q", out)
	}
}

func TestRunBatchSelectsDownloaderBySiteAndWiresDestinations(t *testing.T) {
	restoreBatchFactories(t)
	genericScraper := &fakeDryRunScraper{items: []model.MediaItem{{URL: "https://cyberdrop.cr/item", Title: "generic"}}}
	youtubeScraper := &fakeDryRunScraper{items: []model.MediaItem{{URL: "https://youtube.com/item", Title: "youtube"}}}
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
	if err := runBatch(globalOptions{configPath: filepath.Join(t.TempDir(), "missing.yaml"), noTui: true}, []string{"https://cyberdrop.cr/a/album"}); err != nil {
		t.Fatalf("generic batch failed: %v", err)
	}
	if genericCalls != 1 || youtubeCalls != 0 {
		t.Fatalf("generic batch selected wrong downloader: generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
	if len(genericDL.dests) != 1 || !strings.HasSuffix(genericDL.dests[0], filepath.Join("album")) {
		t.Fatalf("generic batch destination = %v, want album slug under output", genericDL.dests)
	}

	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"youtube": youtubeScraper}, nil
	}
	if err := runBatch(globalOptions{configPath: filepath.Join(t.TempDir(), "missing.yaml"), noTui: true, output: base, outputSet: true}, []string{"https://youtube.com/watch?v=abc"}); err != nil {
		t.Fatalf("youtube batch failed: %v", err)
	}
	if youtubeCalls != 1 || genericCalls != 1 {
		t.Fatalf("youtube batch selected wrong downloader: generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
	if len(youtubeDL.dests) != 1 || !strings.HasSuffix(youtubeDL.dests[0], filepath.Join("watch")) {
		t.Fatalf("youtube batch destination = %v, want watch slug under output", youtubeDL.dests)
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

type mockScraper struct{ items []model.MediaItem }

func (m *mockScraper) Name() string { return "mock" }
func (m *mockScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	return m.items, nil
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

func TestDiskWarningIncludesUnknownItems(t *testing.T) {
	got := diskWarning(3*1024*1024, 1024*1024, 2)
	want := "disk: 1.0M free, 3.0M needed (2.0M short) (2 items with unknown size)"
	if got != want {
		t.Fatalf("diskWarning() = %q, want %q", got, want)
	}
}

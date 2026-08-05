package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// fakeDryRunScraper is a minimal model.Scraper for exercising runDryRun
// without any network access.
type fakeDryRunScraper struct {
	items []model.MediaItem
	err   error
}

func (f *fakeDryRunScraper) Name() string { return "fake" }

func (f *fakeDryRunScraper) Parse(url string) ([]model.MediaItem, error) {
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
	reg := scraperRegistry()
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

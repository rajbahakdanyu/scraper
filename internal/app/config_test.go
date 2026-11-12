package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func validConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{OutputDir: t.TempDir(), ConcurrentDownloads: 3, PerHostConcurrency: 2}
}

func TestFinalizeConfigRejectsInvalidSettings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"zero concurrency", func(c *config.Config) { c.ConcurrentDownloads = 0 }, "--concurrent"},
		{"huge concurrency", func(c *config.Config) { c.ConcurrentDownloads = 257 }, "--concurrent"},
		{"negative per-host", func(c *config.Config) { c.PerHostConcurrency = -1 }, "--per-host"},
		{"quality with audio", func(c *config.Config) { c.Quality, c.AudioOnly = "720p", "m4a" }, "together"},
		{"unknown quality", func(c *config.Config) { c.Quality = "4k" }, "--quality"},
		{"unknown backend", func(c *config.Config) { c.YouTubeBackend = "ffmpeg" }, "--youtube-backend"},
		{"unknown audio format", func(c *config.Config) { c.AudioOnly = "flac" }, "--audio-only"},
		{"bad include glob", func(c *config.Config) { c.Include = []string{"ok*", "[x"} }, "--include pattern \"[x\" (position 2)"},
		{"bad exclude glob", func(c *config.Config) { c.Exclude = []string{"[x"} }, "--exclude"},
		{"bad min size", func(c *config.Config) { c.MinSize = "big" }, "--min-size"},
		{"bad max size", func(c *config.Config) { c.MaxSize = "-1MB" }, "--max-size"},
		{"missing cookies file", func(c *config.Config) { c.Cookies = filepath.Join(c.OutputDir, "none.txt") }, "cookies"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig(t)
			tc.mutate(cfg)
			err := FinalizeConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestFinalizeConfigDerivesValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cookieFile := filepath.Join(home, "cookies.txt")
	if err := os.WriteFile(cookieFile, []byte(".pixeldrain.com\tTRUE\t/\tTRUE\t0\tsession\tabc\n"), 0600); err != nil {
		t.Fatal(err)
	}
	orig := importBrowserCookies
	t.Cleanup(func() { importBrowserCookies = orig })
	var importedFor []string
	importBrowserCookies = func(browser string, domains []string) ([]*http.Cookie, error) {
		importedFor = domains
		return []*http.Cookie{{Name: "browser", Value: browser}}, nil
	}

	cfg := validConfig(t)
	cfg.OutputDir = "~/media"
	cfg.MinSize, cfg.MaxSize = "1K", "2M"
	cfg.Cookies = "~/cookies.txt"
	cfg.CookiesFromBrowser = "firefox"
	if err := FinalizeConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.OutputDir != filepath.Join(home, "media") || cfg.Cookies != cookieFile {
		t.Fatalf("~ not expanded: output %q, cookies %q", cfg.OutputDir, cfg.Cookies)
	}
	if cfg.MinSizeBytes() != 1024 || cfg.MaxSizeBytes() != 2<<20 {
		t.Fatalf("sizes = %d..%d", cfg.MinSizeBytes(), cfg.MaxSizeBytes())
	}
	got := cfg.ParsedCookies()
	if len(got) != 2 || got[0].Name != "session" || got[1].Value != "firefox" {
		t.Fatalf("cookies = %v, want the file's then the browser's", got)
	}
	if len(importedFor) != len(AllDomains()) {
		t.Fatalf("browser import asked for %d domains, want every site's %d", len(importedFor), len(AllDomains()))
	}

	// Finalizing again (the GUI does on every settings change) must not
	// keep stale derived values.
	cfg.MinSize, cfg.MaxSize, cfg.Cookies, cfg.CookiesFromBrowser = "", "", "", ""
	if err := FinalizeConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MinSizeBytes() != 0 || cfg.MaxSizeBytes() != 0 || len(cfg.ParsedCookies()) != 0 {
		t.Fatalf("stale derived values: %d %d %v", cfg.MinSizeBytes(), cfg.MaxSizeBytes(), cfg.ParsedCookies())
	}
}

func TestFinalizeConfigReportsBrowserImportFailure(t *testing.T) {
	orig := importBrowserCookies
	t.Cleanup(func() { importBrowserCookies = orig })
	importBrowserCookies = func(string, []string) ([]*http.Cookie, error) { return nil, errors.New("profile locked") }
	cfg := validConfig(t)
	cfg.CookiesFromBrowser = "firefox"
	if err := FinalizeConfig(cfg); err == nil || !strings.Contains(err.Error(), "profile locked") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseURLListAndGroups(t *testing.T) {
	input := "# comment\n\n https://pixeldrain.com/u/a \n" +
		"https://bunkr.cr/f/item\thttps://bunkr.cr/a/album\tignored\n" +
		"\t \n" +
		"https://youtu.be/dQw4w9WgXcQ\n"
	entries, err := ParseURLList(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{URL: "https://pixeldrain.com/u/a"},
		{URL: "https://bunkr.cr/f/item", AlbumURL: "https://bunkr.cr/a/album"},
		{URL: "https://youtu.be/dQw4w9WgXcQ"},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v", entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}

	yt, generic, err := SplitGroups(entries)
	if err != nil || len(yt) != 1 || len(generic) != 2 || yt[0].URL != want[2].URL {
		t.Fatalf("SplitGroups = %v %v %v", yt, generic, err)
	}
	if _, _, err := SplitGroups([]Entry{{URL: "https://example.com/x"}}); err == nil {
		t.Fatal("an unsupported site must fail the split")
	}
}

func TestReadURLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(path, []byte("https://pixeldrain.com/u/a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadURLFile(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadURLFile = %v, %v", entries, err)
	}
	if _, err := ReadURLFile(path + ".missing"); err == nil || !strings.Contains(err.Error(), "open input file") {
		t.Fatalf("missing file err = %v", err)
	}
}

func TestBuildAlbumsPlacesEntriesInTheirAlbumFolders(t *testing.T) {
	cfg := validConfig(t)
	bunkr := &mockScraper{name: "bunker"}
	reg := map[string]model.Scraper{"bunker": bunkr}
	albums, err := BuildAlbums(cfg, reg, []Entry{
		{URL: "https://bunkr.cr/a/first"},
		{URL: "https://bunkr.cr/f/item", AlbumURL: "https://bunkr.cr/a/second"},
	}, nil, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if albums[0].Slug != "first" || albums[1].Slug != "second" || albums[1].URL != "https://bunkr.cr/f/item" {
		t.Fatalf("albums = %+v", albums)
	}
	if albums[1].Dest != filepath.Join(cfg.OutputDir, "second") || albums[1].Site != "bunker" || albums[1].Scraper.Name() != "bunker" {
		t.Fatalf("album = %+v", albums[1])
	}

	yt := &mockScraper{name: "youtube"}
	albums, err = BuildAlbums(cfg, nil, []Entry{{URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}}, yt, Hooks{})
	if err != nil || albums[0].Site != "youtube" || albums[0].Slug != "dQw4w9WgXcQ" || albums[0].Scraper.Name() != "youtube" {
		t.Fatalf("youtube albums = %+v, %v", albums, err)
	}
}

func TestPreviewAlbum(t *testing.T) {
	ctx := context.Background()
	partial := errors.New("page 2 failed")
	p, err := PreviewAlbum(ctx, &mockScraper{items: []model.MediaItem{{Size: 10}, {Size: 0}, {Size: 5}}, err: partial}, "u", t.TempDir(), false)
	if err != nil || p.TotalSize != 15 || p.Unknown != 1 || len(p.Items) != 3 || !errors.Is(p.Warning, partial) || p.DiskWarning != "" {
		t.Fatalf("partial preview = %+v, %v", p, err)
	}
	p, err = PreviewAlbum(ctx, &mockScraper{err: ErrNoItems}, "u", t.TempDir(), true)
	if err != nil || !p.NoItems {
		t.Fatalf("empty preview = %+v, %v", p, err)
	}
	if _, err = PreviewAlbum(ctx, &mockScraper{err: partial}, "u", t.TempDir(), true); !errors.Is(err, partial) {
		t.Fatalf("failed preview err = %v", err)
	}
	p, err = PreviewAlbum(ctx, &mockScraper{items: []model.MediaItem{{Size: 1 << 62}}}, "u", filepath.Join(t.TempDir(), "missing"), true)
	if err != nil || p.DiskWarning == "" {
		t.Fatalf("disk check skipped: %+v, %v", p, err)
	}
}

func TestCollectScrapeFailure(t *testing.T) {
	boom := errors.New("boom")
	if got := CollectScrapeFailure(boom, "https://bunkr.cr/a/x"); len(got) != 1 || got[0].Item.URL != "https://bunkr.cr/a/x" || got[0].Err != boom {
		t.Fatalf("got %+v", got)
	}
	for _, tc := range []struct {
		err   error
		album string
	}{{nil, "u"}, {&model.CancelledError{URL: "u"}, "u"}, {boom, ""}} {
		if got := CollectScrapeFailure(tc.err, tc.album); got != nil {
			t.Fatalf("CollectScrapeFailure(%v, %q) = %+v, want nil", tc.err, tc.album, got)
		}
	}
}

func TestRunLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f, err := OpenLogFile("~/run.log")
	if err != nil {
		t.Fatal(err)
	}
	WriteLogLine(f, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "first")
	f.Close()
	f, err = OpenLogFile(filepath.Join(home, "run.log"))
	if err != nil {
		t.Fatal(err)
	}
	WriteLogLine(f, time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), "second")
	f.Close()
	data, _ := os.ReadFile(filepath.Join(home, "run.log"))
	if string(data) != "2026-01-02T03:04:05Z first\n2026-01-02T03:04:06Z second\n" {
		t.Fatalf("log = %q (must append)", data)
	}
	if _, err := OpenLogFile(filepath.Join(home, "no", "such", "dir", "run.log")); err == nil {
		t.Fatal("expected an error for a missing log directory")
	}
	if NotificationSummary(nil) != "Download complete" || NotificationSummary(errors.New("x")) != "Download failed" {
		t.Fatal("unexpected notification text")
	}
}

package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

func TestRunPlainNoItemsIsAnError(t *testing.T) {
	var buf bytes.Buffer
	err := runPlain(newFakePlainDownloader(), func() ([]model.MediaItem, error) {
		return nil, app.ErrNoItems
	}, t.TempDir(), "", "", &buf)
	if !errors.Is(err, app.ErrNoItems) {
		t.Fatalf("runPlain should surface app.ErrNoItems, got %v", err)
	}
	if !strings.Contains(buf.String(), "No items found") {
		t.Fatalf("output should still say no items were found: %q", buf.String())
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

func TestRunOnceLoadsConfigOnceAcrossWatchIterations(t *testing.T) {
	calls := 0
	orig := configLoader
	configLoader = func(opts globalOptions) (*config.Config, error) {
		calls++
		return &config.Config{OutputDir: t.TempDir()}, nil
	}
	t.Cleanup(func() { configLoader = orig })

	runOnce := newRunOnce(globalOptions{}, []app.Entry{{URL: "https://unsupported.example/x"}})
	for range 3 {
		if err := runOnce(); err == nil {
			t.Fatal("expected an unsupported-site error")
		}
	}
	if calls != 1 {
		t.Fatalf("config (log file, browser cookies) loaded %d times, want 1", calls)
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

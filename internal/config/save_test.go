package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveRoundTripsAndKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scraper.yaml")
	original := "# my settings\noutput_dir: /tmp/old # where files go\nunknown_key: keep-me\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.OutputDir = "/tmp/new"
	cfg.ConcurrentDownloads = 7
	cfg.DiskCheck = false
	cfg.ScrapeDelay = 500 * time.Millisecond
	cfg.Include = []string{"*.mp4"}

	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# my settings", "# where files go", "unknown_key: keep-me", "scrape_delay: 500ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("saved file lost %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "proxy:") {
		t.Errorf("default-valued keys absent from the file must not be added:\n%s", text)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputDir != "/tmp/new" || got.ConcurrentDownloads != 7 || got.DiskCheck || got.ScrapeDelay != 500*time.Millisecond || len(got.Include) != 1 {
		t.Fatalf("reloaded config = %+v", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSaveCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.yaml")
	cfg := defaultConfig()
	cfg.Proxy = "http://proxy:8080"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Proxy != "http://proxy:8080" || got.Timeout != 30*time.Second {
		t.Fatalf("reloaded config = %+v", got)
	}
}

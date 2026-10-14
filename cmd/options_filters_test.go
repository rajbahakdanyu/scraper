package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
)

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
	if ok || h != 0 {
		t.Errorf("ValidQuality(9999p) = %d, %v; want 0, false", h, ok)
	}
	h, ok = youtube.ValidQuality("")
	if !ok || h != 0 {
		t.Errorf("ValidQuality(\"\") = %d, %v; want 0, true", h, ok)
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

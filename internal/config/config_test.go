package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, ".scraper.yaml")

	content := `output_dir: /tmp/test
concurrent_downloads: 10
timeout: 60s
user_agent: TestAgent/1.0
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.OutputDir != "/tmp/test" {
		t.Errorf("expected output_dir /tmp/test, got %s", cfg.OutputDir)
	}
	if cfg.ConcurrentDownloads != 10 {
		t.Errorf("expected 10 concurrent downloads, got %d", cfg.ConcurrentDownloads)
	}
	if cfg.Timeout != 60*time.Second {
		t.Errorf("expected 60s timeout, got %v", cfg.Timeout)
	}
	if cfg.UserAgent != "TestAgent/1.0" {
		t.Errorf("expected TestAgent/1.0, got %s", cfg.UserAgent)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "nonexistent.yaml")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ConcurrentDownloads != 5 {
		t.Errorf("expected default 5 concurrent downloads, got %d", cfg.ConcurrentDownloads)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("expected default 30s timeout, got %v", cfg.Timeout)
	}
}

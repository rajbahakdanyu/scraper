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
	if cfg.RateLimit != "" {
		t.Errorf("expected default rate limit to be unlimited (empty), got %q", cfg.RateLimit)
	}
	if cfg.SkipChecksums {
		t.Error("expected checksums to be enabled by default")
	}
}

func TestLoadConfigRateLimitAndChecksums(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, ".scraper.yaml")

	content := `rate_limit: 500K
skip_checksums: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.RateLimit != "500K" {
		t.Errorf("expected rate_limit 500K, got %q", cfg.RateLimit)
	}
	if !cfg.SkipChecksums {
		t.Error("expected skip_checksums to be true")
	}
}

func TestParseRateLimit(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"1000", 1000, false},
		{"500K", 500 * 1024, false},
		{"500k", 500 * 1024, false},
		{"2M", 2 * 1024 * 1024, false},
		{"1G", 1024 * 1024 * 1024, false},
		{"1.5M", int64(1.5 * 1024 * 1024), false},
		{"  2M  ", 2 * 1024 * 1024, false},
		{"abc", 0, true},
		{"-5", 0, true},
		{"-5M", 0, true},
	}

	for _, tt := range tests {
		got, err := ParseRateLimit(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseRateLimit(%q) expected an error, got %d", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRateLimit(%q) returned unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseRateLimit(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestLoadExpandsHomeOutputDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("output_dir: ~/Videos\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutputDir != filepath.Join(home, "Videos") {
		t.Fatalf("OutputDir = %q", cfg.OutputDir)
	}
}

func TestLoadDefaultsNonpositiveConcurrency(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("concurrent_downloads: -3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConcurrentDownloads != 5 {
		t.Fatalf("ConcurrentDownloads = %d", cfg.ConcurrentDownloads)
	}
}

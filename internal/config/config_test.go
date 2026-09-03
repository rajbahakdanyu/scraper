package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	output, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

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

func TestLoadWarnsForGroupOrOtherReadableConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("output_dir: /tmp/test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		if _, err := Load(configPath); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stderr, "permissions") {
		t.Fatalf("expected permissions warning, got %q", stderr)
	}
}

func TestLoadDoesNotWarnForPrivateConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("output_dir: /tmp/test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		if _, err := Load(configPath); err != nil {
			t.Fatal(err)
		}
	})
	if stderr != "" {
		t.Fatalf("expected no permissions warning, got %q", stderr)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load("")
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

func TestLoadExplicitMissingConfigErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "typo.yaml")
	if _, err := Load(missing); err == nil {
		t.Fatal("expected an error for an explicitly specified missing config file")
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
		{"Nan", 0, true},
		{"Inf", 0, true},
		{"1e100G", 0, true},
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

func TestConfigProxyRoundTrip(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "proxy: http://proxy.example.com:8080\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy != "http://proxy.example.com:8080" {
		t.Errorf("Proxy = %q, want http://proxy.example.com:8080", cfg.Proxy)
	}
}

func TestConfigProxyEmptyDefault(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy != "" {
		t.Errorf("Proxy default should be empty, got %q", cfg.Proxy)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"", 0},
		{"0", 0},
		{"500K", 500 * 1024},
		{"1M", 1024 * 1024},
		{"1.5M", int64(1.5 * 1024 * 1024)},
		{"2G", 2 * 1024 * 1024 * 1024},
		{"1k", 1024},
	}
	for _, tt := range tests {
		got, err := ParseSize(tt.input)
		if err != nil {
			t.Errorf("ParseSize(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseSizeInvalid(t *testing.T) {
	for _, s := range []string{"-1M", "abc", "1.2.3M"} {
		if _, err := ParseSize(s); err == nil {
			t.Errorf("ParseSize(%q) should error", s)
		}
	}
}

func TestParseSizeRejectsInt64OverflowBoundary(t *testing.T) {
	// 2^63 exactly: float64(math.MaxInt64) rounds to this value, so the old
	// `scaled > math.MaxInt64` guard admitted it and int64() conversion
	// overflowed to a negative number.
	for _, s := range []string{"9223372036854775808", "8796093022208G"} {
		n, err := ParseSize(s)
		if err == nil {
			t.Errorf("ParseSize(%q) = %d, want error (2^63 boundary)", s, n)
		}
	}
	// Values safely below the boundary still parse.
	n, err := ParseSize("1G")
	if err != nil || n != 1<<30 {
		t.Errorf("ParseSize(1G) = %d, %v; want %d, nil", n, err, 1<<30)
	}
}

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	OutputDir           string        `yaml:"output_dir"`
	ConcurrentDownloads int           `yaml:"concurrent_downloads"`
	Timeout             time.Duration `yaml:"timeout"`
	UserAgent           string        `yaml:"user_agent"`
	// RateLimit caps the combined download speed across all concurrent
	// downloads. Accepts a plain byte count or a human-friendly suffixed
	// value like "500K", "2M", "1.5G" (binary/1024-based). Empty or "0"
	// means unlimited. Parse with ParseRateLimit before use.
	RateLimit string `yaml:"rate_limit"`
	// SkipChecksums disables writing/verifying the .sha256 sidecar file
	// that normally accompanies each completed download.
	SkipChecksums bool `yaml:"skip_checksums"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = getDefaultConfigPath()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultConfig(), nil
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.OutputDir == "" {
		cfg.OutputDir = defaultConfig().OutputDir
	}
	if cfg.ConcurrentDownloads == 0 {
		cfg.ConcurrentDownloads = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		OutputDir:           defaultOutputDir(),
		ConcurrentDownloads: 5,
		Timeout:             30 * time.Second,
		UserAgent:           "Scraper/1.0",
		RateLimit:           "",
		SkipChecksums:       false,
	}
}

// defaultOutputDir returns "<home>/Downloads", falling back to "." (the
// current directory) if the home directory can't be determined (e.g. $HOME
// unset) so callers never end up with a bogus root-relative path.
func defaultOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return filepath.Join(home, "Downloads")
}

func getDefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".scraper.yaml"
	}
	return filepath.Join(home, ".scraper.yaml")
}

// ParseRateLimit parses a rate-limit string into bytes/sec. It accepts a
// plain number (raw bytes/sec) or a number suffixed with K/M/G for
// kilobytes/megabytes/gigabytes (binary, 1024-based), e.g. "500K", "2M",
// "1.5G". An empty string or "0" means unlimited, returned as 0.
func ParseRateLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	mult := float64(1)
	numPart := s
	switch s[len(s)-1] {
	case 'k', 'K':
		mult = 1024
		numPart = s[:len(s)-1]
	case 'm', 'M':
		mult = 1024 * 1024
		numPart = s[:len(s)-1]
	case 'g', 'G':
		mult = 1024 * 1024 * 1024
		numPart = s[:len(s)-1]
	}

	n, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid rate limit %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid rate limit %q: must not be negative", s)
	}
	return int64(n * mult), nil
}

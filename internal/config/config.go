package config

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultUserAgent = "Scraper/1.0"

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
	// Checksums enables writing and verifying the .sha256 sidecar file
	// that accompanies each completed download. Off by default; enable
	// with `checksums: true` or --checksums.
	Checksums bool `yaml:"checksums"`
	// Proxy is the HTTP/S proxy URL for all outbound requests.
	// Empty means no proxy (default).
	Proxy string `yaml:"proxy"`
	// Quality caps YouTube video resolution (2160p, 1440p, 1080p, 720p,
	// 480p, 360p, 240p). Empty means best available (default).
	Quality string `yaml:"quality"`
	// AudioOnly downloads only the audio track from YouTube videos.
	// Accepted values: empty (off), "m4a", "mp3".
	AudioOnly string `yaml:"audio_only"`
	// Cookies is the path to a Netscape-format cookies file.
	// Empty means no cookies file (default).
	Cookies string `yaml:"cookies"`
	// cookies holds the parsed cookies at runtime. Set by loadGlobalConfig
	// in cmd/root.go after parsing the file. Not serialized to YAML.
	cookies []*http.Cookie `yaml:"-"`
	// Include specifies glob patterns for filenames to keep (filepath.Match).
	Include []string `yaml:"include"`
	// Exclude specifies glob patterns for filenames to drop.
	Exclude []string `yaml:"exclude"`
	// MinSize is the minimum file size in human-readable format (500K, 2M, 1G).
	MinSize string `yaml:"min_size"`
	// MaxSize is the maximum file size in human-readable format.
	MaxSize string `yaml:"max_size"`
	// DiskCheck enables the disk-space preflight warning before downloading.
	// When true and the total estimated size exceeds free space, a warning
	// is printed to stderr. Downloads continue regardless.
	DiskCheck bool `yaml:"disk_check"`
	// Notify enables OS notification when all downloads complete.
	Notify bool `yaml:"notify"`
	// MediaExtensions lists extra file extensions the --media-only filter
	// keeps, on top of the built-in media and archive set (e.g. cbz, iso).
	MediaExtensions []string `yaml:"media_extensions"`
	// MaxAlbumItems caps how many items a single album may contribute to a
	// run, so a hostile or broken page cannot turn one URL into an endless
	// run. Zero or negative means unlimited.
	MaxAlbumItems int `yaml:"max_album_items"`
	// ScrapeDelay pauses between album scrapes, easing the load on sites
	// that rate-limit page requests. Zero means no delay.
	ScrapeDelay time.Duration `yaml:"scrape_delay"`
	// LogFile appends a timestamped trace of scrape errors and run
	// summaries to this file. Empty means no log file.
	LogFile string `yaml:"log_file"`
	// MediaOnly restricts downloads to media and archive files. On by
	// default; set false (or pass --media-only=false) to download
	// everything an album lists.
	MediaOnly bool `yaml:"media_only"`

	minSizeBytes int64 `yaml:"-"`
	maxSizeBytes int64 `yaml:"-"`
}

func (c *Config) ParsedCookies() []*http.Cookie {
	return c.cookies
}

func (c *Config) SetParsedCookies(cookies []*http.Cookie) {
	c.cookies = cookies
}

func (c *Config) HasFilters() bool {
	return len(c.Include) > 0 || len(c.Exclude) > 0 || c.MinSize != "" || c.MaxSize != ""
}

func (c *Config) MinSizeBytes() int64     { return c.minSizeBytes }
func (c *Config) MaxSizeBytes() int64     { return c.maxSizeBytes }
func (c *Config) SetMinSizeBytes(n int64) { c.minSizeBytes = n }
func (c *Config) SetMaxSizeBytes(n int64) { c.maxSizeBytes = n }

func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		path = getDefaultConfigPath()
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0077 != 0 {
		fmt.Fprintf(os.Stderr, "warning: config file %q has group or other permissions (%04o)\n", path, fi.Mode().Perm())
	}

	// #nosec G304 -- reading an explicitly user-selected local config path is intended behavior.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if explicit {
				return nil, fmt.Errorf("config file %q not found", path)
			}
			return defaultConfig(), nil
		}
		return nil, err
	}

	// Seed from defaults so config-file keys that are omitted keep their
	// defaults (yaml.v3 only overwrites keys present in the document).
	cfg := *defaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	expanded, err := ExpandHomePath(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	cfg.OutputDir = expanded
	// Clamps still apply to explicit zero/negative values in the config
	// file, which overwrite the seeded defaults above.
	if cfg.ConcurrentDownloads <= 0 {
		cfg.ConcurrentDownloads = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &cfg, nil
}

// ExpandHomePath expands ~ and ~/... while rejecting unsupported ~user paths.
func ExpandHomePath(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return "", fmt.Errorf("cannot expand %q: ~user home directories are not supported", path)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("expand %q: home directory unavailable", path)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func defaultConfig() *Config {
	return &Config{
		OutputDir:           defaultOutputDir(),
		ConcurrentDownloads: 5,
		Timeout:             30 * time.Second,
		UserAgent:           DefaultUserAgent,
		RateLimit:           "",
		Checksums:           false,
		MaxAlbumItems:       10000,
		DiskCheck:           true,
		MediaOnly:           true,
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
	n, err := parseSize(s)
	if err != nil {
		return 0, fmt.Errorf("invalid rate limit %q: %v", s, err)
	}
	return n, nil
}

func ParseSize(s string) (int64, error) {
	return parseSize(s)
}

// parseSize parses human-readable byte sizes: 500K, 2M, 1G (1024-based).
func parseSize(s string) (int64, error) {
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
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid size %q: must not be negative", s)
	}
	scaled := n * mult
	// float64(math.MaxInt64) is exactly 2^63, which int64 cannot represent —
	// and any float in (2^63 - 1024, 2^63] rounds to it. Compare against
	// 1<<63 so the boundary itself is rejected before the conversion
	// overflows into a negative value.
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) || scaled >= float64(1<<63) {
		return 0, fmt.Errorf("invalid size %q: out of range", s)
	}
	return int64(scaled), nil
}

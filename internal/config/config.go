package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	OutputDir           string        `yaml:"output_dir"`
	ConcurrentDownloads int           `yaml:"concurrent_downloads"`
	Timeout             time.Duration `yaml:"timeout"`
	UserAgent           string        `yaml:"user_agent"`
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

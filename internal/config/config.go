package config

import (
    "errors"
    "os"
    "time"
    
    "gopkg.in/yaml.v3"
)

type Config struct {
    OutputDir         string        `yaml:"output_dir"`
    ConcurrentDownloads int           `yaml:"concurrent_downloads"`
    Timeout           time.Duration `yaml:"timeout"`
    UserAgent         string        `yaml:"user_agent"`
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
    
    if cfg.ConcurrentDownloads == 0 {
        cfg.ConcurrentDownloads = 5
    }
    if cfg.Timeout == 0 {
        cfg.Timeout = 30 * time.Second
    }
    
    return &cfg, nil
}

func defaultConfig() *Config {
    home, _ := os.UserHomeDir()
    return &Config{
        OutputDir:         home + "/Downloads",
        ConcurrentDownloads: 5,
        Timeout:           30 * time.Second,
        UserAgent:         "Scraper/1.0",
    }
}

func getDefaultConfigPath() string {
    home, _ := os.UserHomeDir()
    return home + "/.scraper.yaml"
}
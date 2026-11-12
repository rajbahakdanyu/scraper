package app

import (
	"fmt"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/downloader"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
)

// BuildDownloader builds the downloader for a run: the YouTube downloader
// (which needs its scraper) for siteName "youtube", else the generic HTTP
// downloader. cfg must be finalized.
func BuildDownloader(scraper model.Scraper, siteName string, cfg *config.Config, hooks Hooks) (model.Downloader, error) {
	rateLimitBytesPerSec, err := config.ParseRateLimit(cfg.RateLimit)
	if err != nil {
		return nil, err
	}
	if siteName == "youtube" {
		ytScraper, ok := scraper.(*youtube.Scraper)
		if !ok {
			return nil, fmt.Errorf("youtube: unexpected scraper type %T", scraper)
		}
		dl, err := youtube.NewDownloaderWithConfig(ytScraper, youtube.DownloaderConfig{
			Concurrency:  cfg.ConcurrentDownloads,
			Timeout:      cfg.Timeout,
			UserAgent:    cfg.UserAgent,
			RateLimit:    rateLimitBytesPerSec,
			SkipChecksum: !cfg.Checksums,
			ProxyURL:     cfg.Proxy,
			Cookies:      cfg.ParsedCookies(),
			Quality:      cfg.Quality,
			AudioOnly:    cfg.AudioOnly,
			Warn:         func(msg string) { hooks.notice("%s", msg) },
		})
		if err != nil {
			return nil, fmt.Errorf("youtube downloader: %w", err)
		}
		return dl, nil
	}

	dl, err := downloader.NewDownloaderWithConfig(
		cfg.ConcurrentDownloads,
		cfg.Timeout,
		cfg.UserAgent,
		cfg.Proxy,
		cfg.ParsedCookies(),
	)
	if err != nil {
		return nil, fmt.Errorf("downloader: %w", err)
	}
	dl.SetRateLimit(rateLimitBytesPerSec)
	dl.SetPerHostLimit(cfg.PerHostConcurrency)
	dl.SetSkipChecksums(!cfg.Checksums)
	return dl, nil
}

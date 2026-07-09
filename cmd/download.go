package cmd

import (
	"fmt"
	"sync"

	"github.com/user/scraper/internal/config"
	"github.com/user/scraper/internal/downloader"
	"github.com/user/scraper/internal/model"
	"github.com/user/scraper/internal/parser/youtube"
)

// youtubeDownloader is an optional interface that a scraper can implement
// to handle its own downloads (e.g. YouTube needs signature deciphering).
type youtubeDownloader interface {
	DownloadVideo(videoID, dest string) error
}

// runScrape downloads pre-parsed items into output. For YouTube, it uses
// the scraper's DownloadVideo method; for everything else, the generic
// HTTP downloader with concurrency.
func runScrape(scraper model.Scraper, items []model.MediaItem, targetURL, output string, cfg *config.Config) error {
	fmt.Printf("Found %d item(s). Downloading to %s (concurrency=%d)...\n", len(items), output, cfg.ConcurrentDownloads)

	if yd, ok := scraper.(youtubeDownloader); ok {
		return runYouTubeDownload(yd, scraper, targetURL, output)
	}

	dl := downloader.NewDownloaderWithConfig(
		cfg.ConcurrentDownloads,
		cfg.Timeout,
		cfg.UserAgent,
	)

	go func() {
		for range dl.Progress() {
		}
	}()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string

	for _, item := range items {
		if item.URL == "" {
			fmt.Printf("skipping (no download URL): %s\n", item.Title)
			continue
		}
		wg.Add(1)
		go func(it model.MediaItem) {
			defer wg.Done()
			if err := dl.Download(it, output); err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("%s: %v", it.Title, err))
				mu.Unlock()
				fmt.Printf("failed: %s (%v)\n", it.Title, err)
				return
			}
			fmt.Printf("done: %s\n", it.Title)
		}(item)
	}
	wg.Wait()

	if len(failures) > 0 {
		return fmt.Errorf("%d of %d downloads failed", len(failures), len(items))
	}

	fmt.Println("All downloads complete.")
	return nil
}

func runYouTubeDownload(yd youtubeDownloader, scraper model.Scraper, targetURL, output string) error {
	yt := youtube.NewYouTubeScraper()
	videoID := yt.ExtractVideoID(targetURL)
	if videoID == "" {
		return fmt.Errorf("%s: could not extract video ID from %s", scraper.Name(), targetURL)
	}

	fmt.Printf("Downloading video %s...\n", videoID)
	if err := yd.DownloadVideo(videoID, output); err != nil {
		return fmt.Errorf("%s: %w", scraper.Name(), err)
	}

	fmt.Println("Download complete.")
	return nil
}

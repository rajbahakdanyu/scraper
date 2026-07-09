package cmd

import (
	"fmt"
	"sync"

	"github.com/user/scraper/internal/downloader"
	"github.com/user/scraper/internal/model"
)

// runScrape parses targetURL with the given scraper and downloads every
// resulting item into output, using up to concurrency simultaneous
// downloads. It blocks until all downloads finish and prints a short
// progress summary to stdout.
func runScrape(scraper model.Scraper, targetURL, output string, concurrency int) error {
	items, err := scraper.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("%s: %w", scraper.Name(), err)
	}

	if len(items) == 0 {
		fmt.Println("No media items found.")
		return nil
	}

	fmt.Printf("Found %d item(s). Downloading to %s (concurrency=%d)...\n", len(items), output, concurrency)

	dl := downloader.NewDownloader(concurrency)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string

	for _, item := range items {
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

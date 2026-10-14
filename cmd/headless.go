package cmd

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// watchSignals shuts the downloader down when SIGINT or SIGTERM arrives.
// The returned done channel closes once the watcher exits — after a
// signal, or when stop is called. stop releases the signal registration
// and its goroutine: without it, every --watch interval leaks one, each
// still receiving every signal.
func watchSignals(dl model.Downloader) (done <-chan struct{}, stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	stopped := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer signal.Stop(signals)
		defer close(finished)
		select {
		case <-signals:
			defer func() { _ = recover() }()
			dl.Shutdown()
		case <-stopped:
		}
	}()
	var once sync.Once
	return finished, func() {
		once.Do(func() {
			signal.Stop(signals)
			close(stopped)
		})
	}
}

func downloadAll(dl model.Downloader, items []model.MediaItem, dest string) []error {
	errs, _ := downloadAllWithPaths(dl, items, dest)
	return errs
}

func downloadAllWithPaths(dl model.Downloader, items []model.MediaItem, dest string) ([]error, map[string]string) {
	errs := make([]error, len(items))
	paths := make(map[string]string)
	var pathsMu sync.Mutex
	var wg sync.WaitGroup
	for i, item := range items {
		if item.URL == "" {
			continue
		}
		dl.Prepare(item)
		wg.Add(1)
		go func(i int, item model.MediaItem) {
			defer wg.Done()
			path, err := dl.Download(item, dest)
			errs[i] = err
			if path != "" {
				pathsMu.Lock()
				paths[item.Key()] = path
				pathsMu.Unlock()
			}
		}(i, item)
	}
	wg.Wait()
	return errs, paths
}

// nonCancelledErrs drops user-initiated cancellations from an error list —
// a Ctrl+C mid-run is not a failure and must not produce exit code 1.
func nonCancelledErrs(errs []error) []error {
	kept := make([]error, 0, len(errs))
	for _, err := range errs {
		if err == nil || model.IsCancelled(err) {
			continue
		}
		kept = append(kept, err)
	}
	return kept
}

type batchScrapeResult struct {
	items []model.MediaItem
	err   error
}

func startBatchScrape(ctx context.Context, abort <-chan struct{}, album model.Album) <-chan batchScrapeResult {
	resultCh := make(chan batchScrapeResult, 1)
	go func() {
		select {
		case <-abort:
			resultCh <- batchScrapeResult{err: context.Canceled}
		default:
			items, err := album.Scraper.Parse(ctx, album.URL)
			resultCh <- batchScrapeResult{items: items, err: err}
		}
	}()
	return resultCh
}

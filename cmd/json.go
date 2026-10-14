package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func writeItemResult(out io.Writer, items []model.MediaItem, errs []error, paths map[string]string, album, quality, audioOnly string) (completed, failed, cancelled, total int, totalBytes int64) {
	for i, item := range items {
		if item.URL == "" {
			continue
		}
		total++
		status := model.StatusCompleted
		var itemErr error
		if errs[i] != nil {
			itemErr = errs[i]
			if model.IsCancelled(itemErr) {
				status = model.StatusCancelled
				cancelled++
			} else {
				status = model.StatusFailed
				failed++
			}
		} else {
			completed++
			if item.Size > 0 {
				totalBytes += item.Size
			}
		}
		event := model.JSONEvent{
			Event: "item", Title: model.TitleOf(item), Size: item.Size,
			Path: paths[item.Key()], Status: model.DownloadStatusString(status),
			Album: album, Quality: quality, AudioOnly: audioOnly,
		}
		if itemErr != nil && status == model.StatusFailed {
			event.Error = itemErr.Error()
		}
		writeJSONLine(out, event)
	}
	return
}

func writeJSONLine(w io.Writer, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
		return
	}
	if _, err = fmt.Fprintln(w, string(data)); err != nil {
		fmt.Fprintf(os.Stderr, "json write error: %v\n", err)
	}
}

// runJSON downloads items from a single album and prints JSON Lines
// (one event per line) to out. Errors and warnings go to stderr.
func runJSON(dl model.Downloader, scrapeFn func() ([]model.MediaItem, error), dest, slug, site, targetURL string, quality, audioOnly string, out io.Writer) error {
	reportDir := dest
	items, err := scrapeFn()
	if err != nil && len(items) == 0 {
		writeJSONLine(out, model.JSONEvent{Event: "error", URL: targetURL, Error: err.Error()})
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	if len(items) == 0 {
		writeJSONLine(out, model.JSONEvent{Event: "error", URL: targetURL, Error: "no media items found"})
		return nil
	}

	model.AssignIDs(items, "")

	if len(items) > 1 && slug != "" {
		albumDest, dErr := fileutil.SafeJoin(dest, slug)
		if dErr != nil {
			writeJSONLine(out, model.JSONEvent{Event: "error", URL: targetURL, Error: dErr.Error()})
			return dErr
		}
		if dErr := fileutil.EnsureDir(albumDest); dErr != nil {
			writeJSONLine(out, model.JSONEvent{Event: "error", URL: targetURL, Error: dErr.Error()})
			return dErr
		}
		dest = albumDest
	}

	writeJSONLine(out, model.JSONEvent{Event: "album_start", Site: site, URL: targetURL, Slug: slug, Total: len(items)})

	signalDone, stopSignals := watchSignals(dl)
	defer stopSignals()

	progressDone := make(chan struct{})
	go func() {
		for range dl.Progress() {
		}
		close(progressDone)
	}()

	var errs []error
	var paths map[string]string
	downloadsDone := make(chan struct{})
	go func() {
		errs, paths = downloadAllWithPaths(dl, items, dest)
		close(downloadsDone)
	}()

	<-downloadsDone
	select {
	case <-signalDone:
	default:
		dl.Shutdown()
	}
	<-progressDone

	completed, failed, cancelled, realItems, totalBytes := writeItemResult(out, items, errs, paths, slug, quality, audioOnly)

	retryPath, summary, rErr := retryReport(reportDir, collectFailures(items, errs, targetURL), time.Now())
	if rErr != nil {
		fmt.Fprintf(os.Stderr, "retry list: %v\n", rErr)
	} else if summary != "" {
		fmt.Fprintln(os.Stderr, summary)
	}

	writeJSONLine(out, model.JSONEvent{
		Event:      "summary",
		Total:      realItems,
		Completed:  completed,
		Failed:     failed,
		Cancelled:  cancelled,
		TotalBytes: totalBytes,
		RetryFile:  retryPath,
	})

	return model.NewPartialError("download", nonCancelledErrs(errs))
}

// runBatchJSON downloads items from multiple albums with one-album scrape
// lookahead, printing JSON Lines events to out as each album's items finish.
func runBatchJSON(dl model.Downloader, albums []model.Album, destRoot string, quality, audioOnly string, out io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	abort := make(chan struct{})
	signalDone, stopSignals := watchSignals(dl)
	defer stopSignals()
	go func() {
		<-signalDone
		close(abort)
		cancel()
	}()

	progress := dl.Progress()
	progressDone := make(chan struct{})
	go func() {
		for range progress {
			// Progress is consumed for live output while batch JSON is running.
		}
		close(progressDone)
	}()

	var allErrs []error
	var grandCompleted, grandFailed, grandCancelled int
	var grandTotalItems int
	var failures []model.FailedItem
	var scrapeCh <-chan batchScrapeResult
	// scrapeNext starts the one-album lookahead scrape after albums[idx].
	scrapeNext := func(idx int) {
		if idx+1 < len(albums) {
			scrapeCh = startBatchScrape(ctx, abort, albums[idx+1])
		}
	}
batchLoop:
	for albumIdx, a := range albums {
		select {
		case <-abort:
			break batchLoop
		default:
		}
		if scrapeCh == nil {
			scrapeCh = startBatchScrape(ctx, abort, a)
		}
		fmt.Fprintf(os.Stderr, "Scraping %s...\n", a.URL)
		result := <-scrapeCh
		items, err := result.items, result.err
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			break batchLoop
		}
		if err != nil && len(items) == 0 {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: err.Error()})
			allErrs = append(allErrs, err)
			scrapeNext(albumIdx)
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		if len(items) == 0 {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: "no media items found"})
			scrapeNext(albumIdx)
			continue
		}

		model.AssignIDs(items, fmt.Sprintf("%d:", albumIdx))

		if err := fileutil.EnsureDir(a.Dest); err != nil {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: err.Error()})
			allErrs = append(allErrs, err)
			scrapeNext(albumIdx)
			continue
		}

		writeJSONLine(out, model.JSONEvent{Event: "album_start", Site: a.Site, URL: a.URL, Slug: a.Slug, Total: len(items)})

		downloadsDone := make(chan struct{})
		var errs []error
		var paths map[string]string
		go func() {
			errs, paths = downloadAllWithPaths(dl, items, a.Dest)
			close(downloadsDone)
		}()
		scrapeNext(albumIdx)

		<-downloadsDone

		failures = append(failures, collectFailures(items, errs, a.URL)...)

		completed, failed, cancelled, albumReal, _ := writeItemResult(out, items, errs, paths, a.Slug, quality, audioOnly)

		grandCompleted += completed
		grandFailed += failed
		grandCancelled += cancelled
		grandTotalItems += albumReal

		if partialErr := model.NewPartialError("album", nonCancelledErrs(errs)); partialErr != nil {
			allErrs = append(allErrs, partialErr)
		}
	}

	select {
	case <-abort:
	case <-signalDone:
	default:
		dl.Shutdown()
	}
	<-progressDone

	retryPath, summary, rErr := retryReport(destRoot, failures, time.Now())
	if rErr != nil {
		fmt.Fprintf(os.Stderr, "retry list: %v\n", rErr)
	} else if summary != "" {
		fmt.Fprintln(os.Stderr, summary)
	}

	writeJSONLine(out, model.JSONEvent{
		Event:     "summary",
		Total:     grandTotalItems,
		Completed: grandCompleted,
		Failed:    grandFailed,
		Cancelled: grandCancelled,
		RetryFile: retryPath,
	})

	return model.NewPartialError("batch", allErrs)
}

func runDryRunJSONWithDiskCheck(scraper model.Scraper, targetURL, siteName, slugOverride string, out io.Writer, outputDir string, diskCheck bool) error {
	items, err := scraper.Parse(context.Background(), targetURL)
	if err != nil && len(items) == 0 {
		if errors.Is(err, ErrNoItems) {
			// Dry-run is informational: a URL that yields nothing
			// emits an empty summary and exits 0.
			data, _ := json.Marshal(map[string]interface{}{
				"dryRun":  true,
				"url":     targetURL,
				"site":    siteName,
				"items":   []model.JSONItem{},
				"summary": map[string]interface{}{"total": 0, "completed": 0, "failed": 0, "cancelled": 0},
			})
			fmt.Fprintln(out, string(data))
			return nil
		}
		data, _ := json.Marshal(map[string]interface{}{
			"dryRun": true,
			"error":  err.Error(),
			"url":    targetURL,
			"site":   siteName,
		})
		fmt.Fprintln(out, string(data))
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	runDiskCheck(items, outputDir, diskCheck)

	var jsonItems []model.JSONItem
	var totalBytes int64
	var unknownSize int
	for _, item := range items {
		ji := model.ItemToJSONItem(item)
		jsonItems = append(jsonItems, ji)
		if item.Size > 0 {
			totalBytes += item.Size
		} else {
			unknownSize++
		}
	}

	slug := slugOverride
	if slug == "" {
		slug = albumSlugFor(siteName, targetURL)
	}
	album := model.JSONAlbum{
		Site:  siteName,
		URL:   targetURL,
		Slug:  slug,
		Items: jsonItems,
	}

	result := map[string]interface{}{
		"dryRun": true,
		"albums": []model.JSONAlbum{album},
		"summary": model.JSONSummary{
			Total:       len(items),
			TotalBytes:  totalBytes,
			UnknownSize: unknownSize,
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("json: encode error: %w", err)
	}
	fmt.Fprintln(out, string(data))
	return nil
}

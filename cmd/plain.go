package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

// progressLine renders one progress event as a plain text line for non-TTY
// output. It returns "" for events that print nothing (pending, cancelled,
// and failed — the real error surfaces when Download returns).
func progressLine(evt model.ProgressEvent) string {
	title := model.TitleOf(evt.Item)
	switch evt.Status {
	case model.StatusDownloading:
		if evt.Total <= 0 {
			return fmt.Sprintf("downloading %s", title)
		}
		pct := evt.Bytes * 100 / evt.Total
		if pct > 100 {
			pct = 100
		}
		return fmt.Sprintf("downloading %s (%d%%)", title, pct)
	case model.StatusCompleted:
		if evt.Filename != "" {
			return fmt.Sprintf("completed %s", evt.Filename)
		}
		return fmt.Sprintf("completed %s", title)
	default:
		return ""
	}
}

// plainPrinter prints progress events as plain lines, suppressing repeats
// of the line an item last printed — byte-level progress events arrive far
// too often to print every one of them.
type plainPrinter struct {
	w    io.Writer
	last map[string]string
}

func newPlainPrinter(w io.Writer) *plainPrinter {
	return &plainPrinter{w: w, last: map[string]string{}}
}

func watchSignals(dl dashboard.Downloader) <-chan struct{} {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer signal.Stop(signals)
		<-signals
		defer func() { _ = recover() }()
		dl.Shutdown()
		close(done)
	}()
	return done
}

func watchPlainShutdown(signals <-chan os.Signal, done <-chan struct{}, shutdown func()) {
	select {
	case <-signals:
		shutdown()
	case <-done:
	}
}

func downloadAll(dl dashboard.Downloader, items []model.MediaItem, dest string) []error {
	errs, _ := downloadAllWithPaths(dl, items, dest)
	return errs
}

func downloadAllWithPaths(dl dashboard.Downloader, items []model.MediaItem, dest string) ([]error, map[string]string) {
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

func assignIDs(items []model.MediaItem, prefix string) {
	model.AssignIDs(items, prefix)
}

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

func (p *plainPrinter) event(evt model.ProgressEvent) {
	line := progressLine(evt)
	if line == "" {
		return
	}
	key := evt.Item.Key()
	if p.last[key] == line {
		return
	}
	p.last[key] = line
	fmt.Fprintln(p.w, line)
}

type batchScrapeResult struct {
	items []model.MediaItem
	err   error
}

func startBatchScrape(ctx context.Context, abort <-chan struct{}, album dashboard.DashAlbumURL) <-chan batchScrapeResult {
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

// runPlain is the non-TTY fallback for runWithScraper: it downloads every
// scraped item while printing one status line per progress event to out,
// consuming the same progress channel the dashboard would. It returns a
// non-nil error when any download fails so the process exits non-zero.
func runPlain(dl dashboard.Downloader, scrapeFn func() ([]model.MediaItem, error), dest, slug string, out io.Writer) error {
	items, err := scrapeFn()
	if err != nil && len(items) == 0 {
		return err
	}
	if err != nil {
		fmt.Fprintf(out, "Warning: %v\n", err)
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "No items found.")
		return nil
	}

	// Mirror the dashboard: positional IDs keep duplicate URLs distinct.
	assignIDs(items, "")

	// Mirror the dashboard: albums download into a slug subfolder.
	if len(items) > 1 && slug != "" {
		albumDest, err := fileutil.SafeJoin(dest, slug)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(albumDest, 0750); err != nil {
			return fmt.Errorf("create album directory: %w", err)
		}
		fi, err := os.Lstat(albumDest)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlinked output directory: %s", albumDest)
		}
		dest = albumDest
	}

	// SIGINT/SIGTERM triggers the same downloader shutdown the TUI quit
	// path uses: in-flight downloads are cancelled and the progress channel
	// closes, which ends the consumer loop below. signal.Notify runs here
	// (not inside the watcher goroutine) so no signal can arrive before the
	// handler is registered.
	_ = watchSignals(dl)

	printer := newPlainPrinter(out)
	errs := make([]error, len(items))
	downloadsDone := make(chan struct{})
	go func() {
		errs = downloadAll(dl, items, dest)
		close(downloadsDone)
	}()

	// Consume progress events until every download has finished or the
	// channel is closed (graceful shutdown), whichever comes first.
	progress := dl.Progress()
	consuming := true
	for consuming {
		select {
		case evt, ok := <-progress:
			if !ok {
				consuming = false
			} else {
				printer.event(evt)
			}
		case <-downloadsDone:
			consuming = false
		}
	}
	<-downloadsDone
	// Print events still buffered when the last download returned.
drain:
	for {
		select {
		case evt, ok := <-progress:
			if !ok {
				break drain
			}
			printer.event(evt)
		default:
			break drain
		}
	}

	var realErrs []error
	for i, item := range items {
		if errs[i] == nil {
			continue
		}
		title := model.TitleOf(item)
		if model.IsCancelled(errs[i]) {
			fmt.Fprintf(out, "interrupted: %s\n", title)
			continue
		}
		fmt.Fprintf(out, "failed %s: %v\n", title, errs[i])
		realErrs = append(realErrs, errs[i])
	}
	return model.NewPartialError("download", realErrs)
}

// runBatchPlain is the non-TTY fallback for runBatch: it scrapes albums with
// one-album lookahead, prints a section header, and downloads every item while
// reporting progress via a shared download queue.
func runBatchPlain(dl dashboard.Downloader, albums []dashboard.DashAlbumURL, destRoot string, out io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	abort := make(chan struct{})
	signalDone := watchSignals(dl)
	go func() {
		<-signalDone
		close(abort)
		cancel()
	}()

	printer := newPlainPrinter(out)
	progress := dl.Progress()
	printCh := make(chan string)
	progressDone := make(chan struct{})
	go func() {
		for printCh != nil || progress != nil {
			select {
			case line, ok := <-printCh:
				if !ok {
					printCh = nil
				} else {
					fmt.Fprint(out, line)
				}
			case evt, ok := <-progress:
				if !ok {
					progress = nil
				} else {
					printer.event(evt)
				}
			}
		}
		close(progressDone)
	}()

	var allErrs []error
	var scrapeCh <-chan batchScrapeResult
batchLoop:
	for albumIdx, a := range albums {
		select {
		case <-abort:
			break batchLoop
		default:
		}
		header := fmt.Sprintf("[%s]", a.Slug)
		printCh <- "\n"
		printCh <- fmt.Sprintf("%s\n", strings.Repeat("─", len(header)))
		printCh <- fmt.Sprintf("%s\n", header)
		printCh <- fmt.Sprintf("%s\n", strings.Repeat("─", len(header)))

		if scrapeCh == nil {
			scrapeCh = startBatchScrape(ctx, abort, a)
		}
		printCh <- fmt.Sprintf("Scraping %s...\n", a.URL)
		result := <-scrapeCh
		items, err := result.items, result.err
		if errors.Is(err, context.Canceled) {
			printCh <- "interrupted\n"
			break batchLoop
		}
		if err != nil && len(items) == 0 {
			printCh <- fmt.Sprintf("failed: %v\n", err)
			allErrs = append(allErrs, err)
			if albumIdx+1 < len(albums) {
				scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
			}
			continue
		}
		if err != nil {
			printCh <- fmt.Sprintf("Warning: %v\n", err)
		}
		if len(items) == 0 {
			printCh <- "No items found.\n"
			if albumIdx+1 < len(albums) {
				scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
			}
			continue
		}

		printCh <- fmt.Sprintf("%d items, downloading to %s\n", len(items), a.Dest)

		assignIDs(items, fmt.Sprintf("%d:", albumIdx))

		if err := os.MkdirAll(a.Dest, 0750); err != nil {
			printCh <- fmt.Sprintf("failed: create album directory: %v\n", err)
			allErrs = append(allErrs, err)
			if albumIdx+1 < len(albums) {
				scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
			}
			continue
		}
		fi, err := os.Lstat(a.Dest)
		if err != nil {
			printCh <- fmt.Sprintf("failed: create album directory: %v\n", err)
			allErrs = append(allErrs, err)
			if albumIdx+1 < len(albums) {
				scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
			}
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			err := fmt.Errorf("refusing to use symlinked output directory: %s", a.Dest)
			printCh <- fmt.Sprintf("failed: create album directory: %v\n", err)
			allErrs = append(allErrs, err)
			continue
		}

		downloadsDone := make(chan struct{})
		var errs []error
		go func() {
			errs = downloadAll(dl, items, a.Dest)
			close(downloadsDone)
		}()
		if albumIdx+1 < len(albums) {
			scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
		}

		<-downloadsDone

		for i, item := range items {
			if errs[i] == nil {
				continue
			}
			title := model.TitleOf(item)
			printCh <- fmt.Sprintf("failed %s: %v\n", title, errs[i])
		}
		if partialErr := model.NewPartialError("album", errs); partialErr != nil {
			allErrs = append(allErrs, partialErr)
		}
	}

	close(printCh)
	select {
	case <-abort:
	case <-signalDone:
	default:
		dl.Shutdown()
	}
	<-progressDone

	return model.NewPartialError("batch", allErrs)
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
func runJSON(dl dashboard.Downloader, scrapeFn func() ([]model.MediaItem, error), dest, slug, site, targetURL string, quality, audioOnly string, out io.Writer) error {
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

	assignIDs(items, "")

	if len(items) > 1 && slug != "" {
		albumDest, dErr := fileutil.SafeJoin(dest, slug)
		if dErr != nil {
			writeJSONLine(out, model.JSONEvent{Event: "error", URL: targetURL, Error: dErr.Error()})
			return dErr
		}
		if dErr := os.MkdirAll(albumDest, 0750); dErr != nil {
			writeJSONLine(out, model.JSONEvent{Event: "error", URL: targetURL, Error: fmt.Sprintf("create album directory: %v", dErr)})
			return dErr
		}
		dest = albumDest
	}

	writeJSONLine(out, model.JSONEvent{Event: "album_start", Site: site, URL: targetURL, Slug: slug, Total: len(items)})

	signalDone := watchSignals(dl)

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

	writeJSONLine(out, model.JSONEvent{
		Event:      "summary",
		Total:      realItems,
		Completed:  completed,
		Failed:     failed,
		Cancelled:  cancelled,
		TotalBytes: totalBytes,
	})

	return model.NewPartialError("download", errs)
}

// runBatchJSON downloads items from multiple albums with one-album scrape
// lookahead, printing JSON Lines events to out as each album's items finish.
func runBatchJSON(dl dashboard.Downloader, albums []dashboard.DashAlbumURL, destRoot string, quality, audioOnly string, out io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	abort := make(chan struct{})
	signalDone := watchSignals(dl)
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
	var scrapeCh <-chan batchScrapeResult
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
			if albumIdx+1 < len(albums) {
				scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
			}
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		if len(items) == 0 {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: "no media items found"})
			if albumIdx+1 < len(albums) {
				scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
			}
			continue
		}

		assignIDs(items, fmt.Sprintf("%d:", albumIdx))

		if err := os.MkdirAll(a.Dest, 0750); err != nil {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: fmt.Sprintf("create album directory: %v", err)})
			allErrs = append(allErrs, err)
			if albumIdx+1 < len(albums) {
				scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
			}
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
		if albumIdx+1 < len(albums) {
			scrapeCh = startBatchScrape(ctx, abort, albums[albumIdx+1])
		}

		<-downloadsDone

		completed, failed, cancelled, albumReal, _ := writeItemResult(out, items, errs, paths, a.Slug, quality, audioOnly)

		grandCompleted += completed
		grandFailed += failed
		grandCancelled += cancelled
		grandTotalItems += albumReal

		if partialErr := model.NewPartialError("album", errs); partialErr != nil {
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

	writeJSONLine(out, model.JSONEvent{
		Event:     "summary",
		Total:     grandTotalItems,
		Completed: grandCompleted,
		Failed:    grandFailed,
		Cancelled: grandCancelled,
	})

	return model.NewPartialError("batch", allErrs)
}

func runDryRunJSONWithDiskCheck(scraper model.Scraper, targetURL, siteName string, out io.Writer, outputDir string, diskCheck bool) error {
	items, err := scraper.Parse(context.Background(), targetURL)
	if err != nil && len(items) == 0 {
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

	slug := albumSlugFor(siteName, targetURL)
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

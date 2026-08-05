package cmd

import (
	"encoding/json"
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
	title := evt.Item.Title
	if title == "" {
		title = evt.Item.URL
	}
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
	for i := range items {
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("%d:%s", i, items[i].URL)
		}
	}

	// Mirror the dashboard: albums download into a slug subfolder.
	if len(items) > 1 && slug != "" {
		albumDest, err := fileutil.SafeJoin(dest, slug)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(albumDest, 0750); err != nil {
			return fmt.Errorf("create album directory: %w", err)
		}
		dest = albumDest
	}

	// SIGINT/SIGTERM triggers the same downloader shutdown the TUI quit
	// path uses: in-flight downloads are cancelled and the progress channel
	// closes, which ends the consumer loop below. signal.Notify runs here
	// (not inside the watcher goroutine) so no signal can arrive before the
	// handler is registered.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-signals:
			dl.Shutdown()
		case <-watcherDone:
		}
	}()

	printer := newPlainPrinter(out)
	errs := make([]error, len(items))
	downloadsDone := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i, item := range items {
			if item.URL == "" {
				continue
			}
			dl.Prepare(item)
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = dl.Download(item, dest)
			}()
		}
		wg.Wait()
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

	for i, item := range items {
		if errs[i] == nil {
			continue
		}
		title := item.Title
		if title == "" {
			title = item.URL
		}
		fmt.Fprintf(out, "failed %s: %v\n", title, errs[i])
	}
	return model.NewPartialError("download", errs)
}

// runBatchPlain is the non-TTY fallback for runBatch: it scrapes each album
// serially, prints a section header, and downloads every item while
// reporting progress via a shared download queue.
func runBatchPlain(dl dashboard.Downloader, albums []dashboard.DashAlbumURL, destRoot string, out io.Writer) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-signals:
			dl.Shutdown()
		case <-watcherDone:
		}
	}()

	printer := newPlainPrinter(out)
	progress := dl.Progress()
	progressDone := make(chan struct{})
	go func() {
		for evt := range progress {
			printer.event(evt)
		}
		close(progressDone)
	}()

	var allErrs []error
	for albumIdx, a := range albums {
		header := fmt.Sprintf("[%s]", a.Slug)
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, strings.Repeat("─", len(header)))
		fmt.Fprintln(out, header)
		fmt.Fprintln(out, strings.Repeat("─", len(header)))

		fmt.Fprintf(out, "Scraping %s...\n", a.URL)
		items, err := a.Scraper.Parse(a.URL)
		if err != nil && len(items) == 0 {
			fmt.Fprintf(out, "failed: %v\n", err)
			allErrs = append(allErrs, err)
			continue
		}
		if err != nil {
			fmt.Fprintf(out, "Warning: %v\n", err)
		}
		if len(items) == 0 {
			fmt.Fprintln(out, "No items found.")
			continue
		}

		fmt.Fprintf(out, "%d items, downloading to %s\n", len(items), a.Dest)

		for i := range items {
			if items[i].ID == "" {
				items[i].ID = fmt.Sprintf("%d:%d:%s", albumIdx, i, items[i].URL)
			}
		}

		if err := os.MkdirAll(a.Dest, 0750); err != nil {
			fmt.Fprintf(out, "failed: create album directory: %v\n", err)
			allErrs = append(allErrs, err)
			continue
		}

		downloadsDone := make(chan struct{})
		errs := make([]error, len(items))
		go func() {
			var wg sync.WaitGroup
			for i, item := range items {
				if item.URL == "" {
					continue
				}
				dl.Prepare(item)
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[i] = dl.Download(item, a.Dest)
				}()
			}
			wg.Wait()
			close(downloadsDone)
		}()

		<-downloadsDone

		for i, item := range items {
			if errs[i] == nil {
				continue
			}
			title := item.Title
			if title == "" {
				title = item.URL
			}
			fmt.Fprintf(out, "failed %s: %v\n", title, errs[i])
		}
		if partialErr := model.NewPartialError("album", errs); partialErr != nil {
			allErrs = append(allErrs, partialErr)
		}
	}

	dl.Shutdown()
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

	for i := range items {
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("%d:%s", i, items[i].URL)
		}
	}

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

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-signals:
			dl.Shutdown()
		case <-watcherDone:
		}
	}()

	progress := dl.Progress()
	paths := make(map[string]string)
	progressDone := make(chan struct{})
	go func() {
		for evt := range progress {
			if evt.Status == model.StatusCompleted && evt.Filename != "" {
				key := evt.Item.Key()
				paths[key] = evt.Filename
			}
		}
		close(progressDone)
	}()

	errs := make([]error, len(items))
	downloadsDone := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i, item := range items {
			if item.URL == "" {
				continue
			}
			dl.Prepare(item)
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = dl.Download(item, dest)
			}()
		}
		wg.Wait()
		close(downloadsDone)
	}()

	<-downloadsDone
	dl.Shutdown()
	<-progressDone

	var completed, failed, cancelled int
	var totalBytes int64
	realItems := 0
	for i, item := range items {
		if item.URL == "" {
			continue
		}
		realItems++
		path := paths[item.Key()]
		if errs[i] != nil {
			if isCancelledError(errs[i]) {
				writeJSONLine(out, model.JSONEvent{
					Event:     "item",
					Title:     itemTitle(item),
					Size:      item.Size,
					Path:      path,
					Status:    model.DownloadStatusString(model.StatusCancelled),
					Album:     slug,
					Quality:   quality,
					AudioOnly: audioOnly,
				})
				cancelled++
			} else {
				writeJSONLine(out, model.JSONEvent{
					Event:     "item",
					Title:     itemTitle(item),
					Size:      item.Size,
					Path:      path,
					Status:    model.DownloadStatusString(model.StatusFailed),
					Album:     slug,
					Error:     errs[i].Error(),
					Quality:   quality,
					AudioOnly: audioOnly,
				})
				failed++
			}
			continue
		}
		writeJSONLine(out, model.JSONEvent{
			Event:     "item",
			Title:     itemTitle(item),
			Size:      item.Size,
			Path:      path,
			Status:    model.DownloadStatusString(model.StatusCompleted),
			Album:     slug,
			Quality:   quality,
			AudioOnly: audioOnly,
		})
		completed++
		if item.Size > 0 {
			totalBytes += item.Size
		}
	}

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

// runBatchJSON downloads items from multiple albums serially, printing JSON
// Lines events to out as each album's items finish.
func runBatchJSON(dl dashboard.Downloader, albums []dashboard.DashAlbumURL, destRoot string, quality, audioOnly string, out io.Writer) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-signals:
			dl.Shutdown()
		case <-watcherDone:
		}
	}()

	progress := dl.Progress()
	paths := make(map[string]string)
	var pathsMu sync.Mutex
	progressDone := make(chan struct{})
	go func() {
		for evt := range progress {
			if evt.Status == model.StatusCompleted && evt.Filename != "" {
				pathsMu.Lock()
				paths[evt.Item.Key()] = evt.Filename
				pathsMu.Unlock()
			}
		}
		close(progressDone)
	}()

	var allErrs []error
	var grandCompleted, grandFailed, grandCancelled int
	var grandTotalItems int
	for albumIdx, a := range albums {
		fmt.Fprintf(os.Stderr, "Scraping %s...\n", a.URL)
		items, err := a.Scraper.Parse(a.URL)
		if err != nil && len(items) == 0 {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: err.Error()})
			allErrs = append(allErrs, err)
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		if len(items) == 0 {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: "no media items found"})
			continue
		}

		for i := range items {
			if items[i].ID == "" {
				items[i].ID = fmt.Sprintf("%d:%d:%s", albumIdx, i, items[i].URL)
			}
		}

		if err := os.MkdirAll(a.Dest, 0750); err != nil {
			writeJSONLine(out, model.JSONEvent{Event: "album_error", Site: a.Site, URL: a.URL, Slug: a.Slug, Error: fmt.Sprintf("create album directory: %v", err)})
			allErrs = append(allErrs, err)
			continue
		}

		writeJSONLine(out, model.JSONEvent{Event: "album_start", Site: a.Site, URL: a.URL, Slug: a.Slug, Total: len(items)})

		downloadsDone := make(chan struct{})
		errs := make([]error, len(items))
		go func() {
			var wg sync.WaitGroup
			for i, item := range items {
				if item.URL == "" {
					continue
				}
				dl.Prepare(item)
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[i] = dl.Download(item, a.Dest)
				}()
			}
			wg.Wait()
			close(downloadsDone)
		}()

		<-downloadsDone

		var completed, failed, cancelled int
		albumReal := 0
		for i, item := range items {
			if item.URL == "" {
				continue
			}
			albumReal++
			pathsMu.Lock()
			path := paths[item.Key()]
			pathsMu.Unlock()
			if errs[i] != nil {
				if isCancelledError(errs[i]) {
					writeJSONLine(out, model.JSONEvent{
						Event:     "item",
						Title:     itemTitle(item),
						Size:      item.Size,
						Path:      path,
						Status:    model.DownloadStatusString(model.StatusCancelled),
						Album:     a.Slug,
						Quality:   quality,
						AudioOnly: audioOnly,
					})
					cancelled++
				} else {
					writeJSONLine(out, model.JSONEvent{
						Event:     "item",
						Title:     itemTitle(item),
						Size:      item.Size,
						Path:      path,
						Status:    model.DownloadStatusString(model.StatusFailed),
						Album:     a.Slug,
						Error:     errs[i].Error(),
						Quality:   quality,
						AudioOnly: audioOnly,
					})
					failed++
				}
				continue
			}
			writeJSONLine(out, model.JSONEvent{
				Event:     "item",
				Title:     itemTitle(item),
				Size:      item.Size,
				Path:      path,
				Status:    model.DownloadStatusString(model.StatusCompleted),
				Album:     a.Slug,
				Quality:   quality,
				AudioOnly: audioOnly,
			})
			completed++
		}

		grandCompleted += completed
		grandFailed += failed
		grandCancelled += cancelled
		grandTotalItems += albumReal

		if partialErr := model.NewPartialError("album", errs); partialErr != nil {
			allErrs = append(allErrs, partialErr)
		}
	}

	dl.Shutdown()
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

// runDryRunJSON scrapes targetURL and prints a single JSON document with
// item metadata — no downloading.
func runDryRunJSON(scraper model.Scraper, targetURL, siteName string, out io.Writer) error {
	items, err := scraper.Parse(targetURL)
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

func itemTitle(item model.MediaItem) string {
	if item.Title != "" {
		return item.Title
	}
	return item.URL
}

func isCancelledError(err error) bool {
	return strings.Contains(err.Error(), "cancelled") || strings.Contains(err.Error(), "canceled")
}

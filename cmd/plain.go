package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
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
func runPlain(dl model.Downloader, scrapeFn func() ([]model.MediaItem, error), dest, slug, sourceURL string, out io.Writer) error {
	reportDir := dest
	items, err := scrapeFn()
	if err != nil && len(items) == 0 {
		if errors.Is(err, app.ErrNoItems) {
			fmt.Fprintln(out, "No items found.")
		}
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
	model.AssignIDs(items, "")

	// Mirror the dashboard: albums download into a slug subfolder.
	if len(items) > 1 && slug != "" {
		albumDest, err := fileutil.SafeJoin(dest, slug)
		if err != nil {
			return err
		}
		if err := fileutil.EnsureDir(albumDest); err != nil {
			return err
		}
		dest = albumDest
	}

	// SIGINT/SIGTERM triggers the same downloader shutdown the TUI quit
	// path uses: in-flight downloads are cancelled and the progress channel
	// closes, which ends the consumer loop below. signal.Notify runs here
	// (not inside the watcher goroutine) so no signal can arrive before the
	// handler is registered.
	_, stopSignals := watchSignals(dl)
	defer stopSignals()

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
	if _, summary, rErr := retryReport(reportDir, app.CollectFailures(items, errs, sourceURL), time.Now()); rErr != nil {
		fmt.Fprintf(out, "retry list: %v\n", rErr)
	} else if summary != "" {
		fmt.Fprintln(out, summary)
	}
	return model.NewPartialError("download", realErrs)
}

// runBatchPlain is the non-TTY fallback for runBatch: it scrapes albums with
// one-album lookahead, prints a section header, and downloads every item while
// reporting progress via a shared download queue.
func runBatchPlain(dl model.Downloader, albums []model.Album, destRoot string, out io.Writer) error {
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
			if errors.Is(err, app.ErrNoItems) {
				printCh <- "No items found.\n"
			} else {
				printCh <- fmt.Sprintf("failed: %v\n", err)
			}
			allErrs = append(allErrs, err)
			scrapeNext(albumIdx)
			continue
		}
		if err != nil {
			printCh <- fmt.Sprintf("Warning: %v\n", err)
		}
		if len(items) == 0 {
			printCh <- "No items found.\n"
			scrapeNext(albumIdx)
			continue
		}

		printCh <- fmt.Sprintf("%d items, downloading to %s\n", len(items), a.Dest)

		model.AssignIDs(items, fmt.Sprintf("%d:", albumIdx))

		if err := fileutil.EnsureDir(a.Dest); err != nil {
			printCh <- fmt.Sprintf("failed: %v\n", err)
			allErrs = append(allErrs, err)
			scrapeNext(albumIdx)
			continue
		}

		downloadsDone := make(chan struct{})
		var errs []error
		go func() {
			errs = downloadAll(dl, items, a.Dest)
			close(downloadsDone)
		}()
		scrapeNext(albumIdx)

		<-downloadsDone

		failures = append(failures, app.CollectFailures(items, errs, a.URL)...)

		for i, item := range items {
			if errs[i] == nil {
				continue
			}
			if model.IsCancelled(errs[i]) {
				continue
			}
			title := model.TitleOf(item)
			printCh <- fmt.Sprintf("failed %s: %v\n", title, errs[i])
		}
		if partialErr := model.NewPartialError("album", nonCancelledErrs(errs)); partialErr != nil {
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

	if _, summary, rErr := retryReport(destRoot, failures, time.Now()); rErr != nil {
		fmt.Fprintf(out, "retry list: %v\n", rErr)
	} else if summary != "" {
		fmt.Fprintln(out, summary)
	}

	return model.NewPartialError("batch", allErrs)
}

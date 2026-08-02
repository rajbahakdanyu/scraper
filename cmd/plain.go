package cmd

import (
	"fmt"
	"io"
	"os"
	"os/signal"
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

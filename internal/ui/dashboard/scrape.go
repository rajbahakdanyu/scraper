package dashboard

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func (d *Dashboard) handleScrapeResult(msg scrapeResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil && len(msg.items) == 0 {
		d.loadingErr = msg.err.Error()
		d.appendError(msg.err.Error())
		d.phase = phaseDone
		return d, nil
	}
	if len(msg.items) == 0 {
		d.loadingErr = "no media items found"
		d.phase = phaseDone
		return d, nil
	}
	if msg.err != nil {
		d.setStatus("warning: %v", msg.err)
		d.appendError(msg.err.Error())
	}

	d.states = make([]itemState, len(msg.items))
	for i := range msg.items {
		model.AssignIDs(msg.items[i:i+1], fmt.Sprintf("%d:", i))
		it := msg.items[i]
		d.states[i] = itemState{item: it, status: model.StatusPending, total: it.Size, sourceURL: d.sourceURL}
	}

	// Album (multiple files): create a subfolder from the URL slug
	if len(msg.items) > 1 && d.albumSlug != "" {
		albumDest, err := fileutil.SafeJoin(d.dest, d.albumSlug)
		if err != nil {
			d.states = nil
			d.loadingErr = err.Error()
			d.phase = phaseDone
			return d, nil
		}
		if err := fileutil.EnsureDir(albumDest); err != nil {
			d.states = nil
			d.loadingErr = err.Error()
			d.phase = phaseDone
			return d, nil
		}
		d.dest = albumDest
	}

	d.phase = phaseDownload
	d.rebuildVisible()
	d.refreshRows()
	d.fitTableHeight()

	var cmds []tea.Cmd
	if d.downloader != nil {
		go d.drainProgress()
		cmds = append(cmds, d.startAllDownloads(), d.ensureRefreshTicker())
	}
	return d, tea.Batch(cmds...)
}

// drainProgress reads from the downloader's progress channel in a loop
// and populates the pending map. It exits when the downloader closes the
// channel (Shutdown). Progress is applied to the UI by the refresh ticker.
func (d *Dashboard) drainProgress() {
	if d.downloader == nil {
		return
	}
	ch := d.downloader.Progress()
	for evt := range ch {
		d.progressMu.Lock()
		if d.pending == nil {
			d.pending = make(map[string]model.ProgressEvent)
		}
		d.pending[evt.Item.Key()] = evt
		d.progressMu.Unlock()
	}
}

func (d *Dashboard) ensureRefreshTicker() tea.Cmd {
	if d.refreshTickActive {
		return nil
	}
	d.refreshTickActive = true
	return func() tea.Msg { return refreshTickMsg{} }
}

func (d *Dashboard) hasPending() bool {
	d.progressMu.Lock()
	defer d.progressMu.Unlock()
	return len(d.pending) > 0
}

func (d *Dashboard) applyPendingProgress() {
	d.progressMu.Lock()
	pending := d.pending
	d.pending = make(map[string]model.ProgressEvent)
	d.progressMu.Unlock()
	if len(pending) == 0 {
		return
	}
	for _, evt := range pending {
		d.applyProgress(evt)
	}
}

func (d *Dashboard) handleBatchScrape(msg batchScrapeMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil && len(msg.items) == 0 {
		d.loadingErr = fmt.Sprintf("%s: %v", msg.album.URL, msg.err)
		d.scrapeErrs = append(d.scrapeErrs, errors.New(d.loadingErr))
		d.appendError(d.loadingErr)
		d.batchRemaining--
		d.batchIdx++
		if d.batchRemaining == 0 && len(d.states) == 0 {
			d.phase = phaseDone
			return d, nil
		}
		return d, d.startNextScrape()
	}

	if msg.err != nil {
		d.setStatus("warning: %v", msg.err)
		d.appendError(fmt.Sprintf("%s: %v", msg.album.URL, msg.err))
	}

	d.batchRemaining--
	d.batchIdx++

	if len(msg.items) == 0 {
		if msg.err != nil {
			d.setStatus("warning: %v", msg.err)
		}
		d.refreshRows()
		if d.batchRemaining > 0 {
			return d, d.startNextScrape()
		}
		if len(d.states) == 0 {
			d.phase = phaseDone
		}
		return d, nil
	}

	wasFirstAlbum := d.phase == phaseLoading

	if err := fileutil.EnsureDir(msg.album.Dest); err != nil {
		d.loadingErr = err.Error()
		d.scrapeErrs = append(d.scrapeErrs, err)
		d.phase = phaseDone
		return d, nil
	}

	baseIdx := len(d.states)
	headerState := itemState{
		isHeader:  true,
		albumName: msg.album.Slug,
		albumSite: msg.album.Site,
	}
	d.states = append(d.states, headerState)

	for i := range msg.items {
		model.AssignIDs(msg.items[i:i+1], fmt.Sprintf("%d:%d:", d.batchIdx-1, i))
		it := msg.items[i]
		st := itemState{
			item:      it,
			total:     it.Size,
			dest:      msg.album.Dest,
			sourceURL: msg.album.URL,
		}
		if it.URL == "" {
			st.status = model.StatusCompleted
		} else {
			st.status = model.StatusPending
		}
		d.states = append(d.states, st)
	}

	if wasFirstAlbum {
		d.phase = phaseDownload
		d.rebuildVisible()
		d.refreshRows()
		d.fitTableHeight()

		var cmds []tea.Cmd
		if d.downloader != nil {
			go d.drainProgress()
			cmds = append(cmds, d.startAllDownloads(), d.ensureRefreshTicker())
		}
		if d.batchRemaining > 0 {
			cmds = append(cmds, d.startNextScrape())
		}
		return d, tea.Batch(cmds...)
	}

	// Subsequent album: items were appended. Start downloads only for the
	// new items, and kick off the next scrape if applicable.
	d.rebuildVisible()
	d.refreshRows()
	d.fitTableHeight()
	totalRows := len(d.states)

	var cmds []tea.Cmd
	if d.downloader != nil {
		for i := baseIdx + 1; i < totalRows; i++ {
			if d.states[i].item.URL == "" {
				continue
			}
			d.downloader.Prepare(d.states[i].item)
			cmds = append(cmds, startDownload(d.downloader, d.states[i].item, d.states[i].dest))
		}
	}
	if d.batchRemaining > 0 {
		cmds = append(cmds, d.startNextScrape())
	}
	return d, tea.Batch(cmds...)
}

func (d *Dashboard) startAllDownloads() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	var cmds []tea.Cmd
	for i := range d.states {
		if d.states[i].isHeader {
			continue
		}
		if d.states[i].item.URL == "" {
			continue
		}
		d.downloader.Prepare(d.states[i].item)
		dest := d.states[i].dest
		if dest == "" {
			dest = d.dest
		}
		cmds = append(cmds, startDownload(d.downloader, d.states[i].item, dest))
	}
	d.refreshRows()
	return tea.Batch(cmds...)
}

func (d *Dashboard) applyProgress(evt model.ProgressEvent) {
	d.ensureStateIndex()
	i, ok := d.stateIndex[evt.Item.Key()]
	if !ok {
		return
	}
	// Terminal states are authoritative: a progress event that was
	// buffered before the item finished (cancelled, completed, failed)
	// must not resurrect it back to an active state.
	switch d.states[i].status {
	case model.StatusCompleted, model.StatusFailed, model.StatusCancelled:
		return
	}
	// Terminal status values (Failed, Cancelled) must only come from
	// applyDone, not from buffered progress events — a stale
	// StatusFailed event queued before a retry would otherwise
	// corrupt the retried item.
	switch evt.Status {
	case model.StatusFailed, model.StatusCancelled:
		return
	case model.StatusCompleted:
		evt.Status = model.StatusDownloading
	}
	if d.states[i].started.IsZero() {
		d.states[i].started = time.Now()
	}
	elapsed := time.Since(d.states[i].started).Seconds()
	d.states[i].status = evt.Status
	d.states[i].bytes = evt.Bytes
	if evt.Total > 0 {
		d.states[i].total = evt.Total
	}
	if elapsed > 0 && evt.Bytes > 0 {
		d.states[i].speed = float64(evt.Bytes) / elapsed
	}
}

func (d *Dashboard) applyDone(msg downloadDoneMsg) {
	d.ensureStateIndex()
	i, ok := d.stateIndex[msg.key]
	if !ok {
		return
	}
	switch {
	case msg.err == nil:
		d.states[i].status = model.StatusCompleted
		d.states[i].lastErr = nil
		if d.states[i].total > 0 {
			d.states[i].bytes = d.states[i].total
		}
	case d.states[i].status == model.StatusCancelled:
		// Already marked cancelled via the progress channel; a
		// cancelled download also returns an error here, but that's
		// not a failure worth downgrading the status for.
	default:
		d.states[i].status = model.StatusFailed
		d.states[i].lastErr = msg.err
		d.setStatus("✗ %s: %v", model.TitleOf(d.states[i].item), msg.err)
	}
}

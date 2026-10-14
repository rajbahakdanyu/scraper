package dashboard

import (
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func (d *Dashboard) moveUpSkippingHeaders() {
	if d.visibleReady && len(d.visible) == 0 {
		return
	}
	if len(d.visible) > 0 {
		idx := d.table.Cursor() - 1
		for idx >= 0 && d.skipHeader(idx) {
			idx--
		}
		if idx < 0 {
			for idx = 0; idx < len(d.visible) && d.skipHeader(idx); idx++ {
			}
		}
		if idx >= 0 && idx < len(d.visible) {
			d.moveCursorTo(idx)
		}
		return
	}
	d.table.MoveUp(1)
	for idx := d.table.Cursor(); idx > 0 && idx < len(d.states) && d.states[idx].isHeader; {
		d.table.MoveUp(1)
		idx = d.table.Cursor()
	}
	// The scan can stop at row 0 when that row is a header (there is no
	// non-header row above it). Park on the first non-header row instead
	// of leaving the cursor on the header.
	if idx := d.table.Cursor(); idx == 0 && len(d.states) > 0 && d.states[0].isHeader {
		for i := 1; i < len(d.states); i++ {
			if !d.states[i].isHeader {
				d.table.SetCursor(i)
				break
			}
		}
	}
}

func (d *Dashboard) moveDownSkippingHeaders() {
	if d.visibleReady && len(d.visible) == 0 {
		return
	}
	if len(d.visible) > 0 {
		idx := d.table.Cursor() + 1
		for idx < len(d.visible) && d.skipHeader(idx) {
			idx++
		}
		if idx >= len(d.visible) {
			idx = d.table.Cursor()
		}
		if idx >= 0 && idx < len(d.visible) {
			d.moveCursorTo(idx)
		}
		return
	}
	d.table.MoveDown(1)
	idx := d.table.Cursor()
	for idx >= 0 && idx < len(d.states) && d.states[idx].isHeader {
		d.table.MoveDown(1)
		idx = d.table.Cursor()
	}
}

// cancelSelected cancels the currently-highlighted item's download, if
// it's still in progress.
func (d *Dashboard) cancelSelected() {
	if d.downloader == nil {
		return
	}
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return
	}
	if d.states[idx].isHeader {
		return
	}
	if d.states[idx].status != model.StatusPending && d.states[idx].status != model.StatusDownloading {
		return
	}
	d.downloader.Cancel(d.states[idx].item)
	d.states[idx].status = model.StatusCancelled
	d.rebuildVisible()
	d.refreshRows()
}

func (d *Dashboard) cancelAll() {
	if d.downloader == nil {
		return
	}
	for i := range d.states {
		st := &d.states[i]
		if st.isHeader || (st.status != model.StatusPending && st.status != model.StatusDownloading) {
			continue
		}
		d.downloader.Cancel(st.item)
		st.status = model.StatusCancelled
	}
	d.rebuildVisible()
	d.refreshRows()
}

func (d *Dashboard) inspectSelected() {
	d.inspectMsg = ""
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return
	}
	if d.states[idx].isHeader {
		return
	}
	if d.states[idx].status != model.StatusFailed {
		return
	}
	if d.states[idx].lastErr != nil {
		d.inspectMsg = displayText(d.states[idx].lastErr.Error())
	}
}

// FailedItem pairs a media item with the error that ended its download and
// the URL it was scraped from.
type FailedItem struct {
	Item      model.MediaItem
	Err       error
	SourceURL string
}

// Result reports how the run ended, mirroring the plain/JSON runners for
// the CLI's exit status: a failed single-URL scrape is a plain error, and
// failed items or batch albums are a *model.PartialError.
func (d *Dashboard) Result() error {
	if !d.batchMode && d.loadingErr != "" {
		return errors.New(d.loadingErr)
	}
	errs := append([]error(nil), d.scrapeErrs...)
	for _, f := range d.Failures() {
		errs = append(errs, f.Err)
	}
	return model.NewPartialError("download", errs)
}

// Failures returns the items that ended in the failed state, in display
// order. The CLI uses it to write a retry list after the TUI exits.
func (d *Dashboard) Failures() []FailedItem {
	var out []FailedItem
	for i := range d.states {
		if d.states[i].isHeader || d.states[i].status != model.StatusFailed {
			continue
		}
		out = append(out, FailedItem{
			Item:      d.states[i].item,
			Err:       d.states[i].lastErr,
			SourceURL: d.states[i].sourceURL,
		})
	}
	return out
}

// handleSearchKey edits the search query while `/` search is active.
func (d *Dashboard) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		d.searching = false
		d.searchQuery = ""
	case "enter":
		d.searching = false
		d.jumpToMatch()
	case "backspace":
		if runes := []rune(d.searchQuery); len(runes) > 0 {
			d.searchQuery = string(runes[:len(runes)-1])
		}
	default:
		if len(msg.Runes) > 0 {
			d.searchQuery += string(msg.Runes)
		}
	}
	return d, nil
}

// jumpToMatch moves the cursor to the next item whose title contains the
// search query, wrapping around and skipping headers.
func (d *Dashboard) jumpToMatch() {
	if d.searchQuery == "" || len(d.states) == 0 {
		return
	}
	query := strings.ToLower(d.searchQuery)
	d.jumpIf(func(st itemState) bool { return strings.Contains(strings.ToLower(model.TitleOf(st.item)), query) })
}

func (d *Dashboard) jumpIf(match func(itemState) bool) {
	if len(d.visible) == 0 {
		return
	}
	start := d.table.Cursor()
	for step := 1; step <= len(d.visible); step++ {
		row := (start + step) % len(d.visible)
		idx := d.stateAt(row)
		st := d.states[idx]
		if !st.isHeader && match(st) {
			d.selectRow(row)
			return
		}
	}
}

// retryAllFailed resets every failed item and starts fresh downloads for
// them, so a batch's failures can be retried without hunting for each row.
func (d *Dashboard) retryAllFailed() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	var cmds []tea.Cmd
	for i := range d.states {
		if d.states[i].isHeader || d.states[i].status != model.StatusFailed {
			continue
		}
		dest := d.states[i].dest
		if dest == "" {
			dest = d.dest
		}
		d.states[i].status = model.StatusPending
		d.states[i].bytes = 0
		d.states[i].total = d.states[i].item.Size
		d.states[i].speed = 0
		d.states[i].started = time.Time{}
		d.states[i].lastErr = nil
		d.downloader.Prepare(d.states[i].item)
		cmds = append(cmds, startDownload(d.downloader, d.states[i].item, dest))
	}
	if len(cmds) == 0 {
		return nil
	}
	d.rebuildVisible()
	d.refreshRows()
	return tea.Batch(append(cmds, d.ensureRefreshTicker())...)
}

// retrySelected resets the selected failed item back to pending and
// kicks off a fresh download attempt.
func (d *Dashboard) retrySelected() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return nil
	}
	if d.states[idx].isHeader {
		return nil
	}
	if d.states[idx].status != model.StatusFailed {
		return nil
	}
	dest := d.states[idx].dest
	if dest == "" {
		dest = d.dest
	}
	d.states[idx].status = model.StatusPending
	d.states[idx].bytes = 0
	d.states[idx].total = d.states[idx].item.Size
	d.states[idx].speed = 0
	d.states[idx].started = time.Time{}
	d.states[idx].lastErr = nil
	d.downloader.Prepare(d.states[idx].item)
	d.rebuildVisible()
	d.refreshRows()
	return tea.Batch(startDownload(d.downloader, d.states[idx].item, dest), d.ensureRefreshTicker())
}

func (d *Dashboard) autoFoldCompletedAlbums() {
	if d.folded == nil {
		d.folded = make(map[int]bool)
	}
	if d.manuallyUnfolded == nil {
		d.manuallyUnfolded = make(map[int]bool)
	}
	changed := false
	for i, st := range d.states {
		if !st.isHeader || d.manuallyUnfolded[i] || d.folded[i] {
			continue
		}
		end := i + 1
		failed, any, complete := false, false, true
		for end < len(d.states) && !d.states[end].isHeader {
			child := d.states[end]
			any = true
			if child.status == model.StatusFailed {
				failed = true
			}
			if child.status == model.StatusDownloading || child.status == model.StatusPending {
				complete = false
			}
			end++
		}
		if any && complete && !failed {
			d.folded[i] = true
			changed = true
		}
	}
	if changed {
		d.rebuildVisible()
	}
}

func (d *Dashboard) toggleFoldAtCursor() {
	if len(d.states) == 0 {
		return
	}
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return
	}
	header := idx
	for header >= 0 && !d.states[header].isHeader {
		header--
	}
	if header < 0 {
		return
	}
	if d.folded[header] {
		d.folded[header] = false
		d.manuallyUnfolded[header] = true
	} else {
		d.folded[header] = true
	}
	d.rebuildVisible()
	d.refreshRows()
	d.fitTableHeight()
}

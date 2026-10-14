package dashboard

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/session"
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
	for idx := d.table.Cursor(); idx > 0 && idx < len(d.Items) && d.Items[idx].Header; {
		d.table.MoveUp(1)
		idx = d.table.Cursor()
	}
	// The scan can stop at row 0 when that row is a header (there is no
	// non-header row above it). Park on the first non-header row instead
	// of leaving the cursor on the header.
	if idx := d.table.Cursor(); idx == 0 && len(d.Items) > 0 && d.Items[0].Header {
		for i := 1; i < len(d.Items); i++ {
			if !d.Items[i].Header {
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
	for idx >= 0 && idx < len(d.Items) && d.Items[idx].Header {
		d.table.MoveDown(1)
		idx = d.table.Cursor()
	}
}

// cancelSelected cancels the highlighted item's download, if it's still
// pending or in progress.
func (d *Dashboard) cancelSelected() {
	if d.Cancel(d.stateAt(d.table.Cursor())) {
		d.rebuildVisible()
		d.refreshRows()
	}
}

func (d *Dashboard) cancelAll() {
	if d.Downloader == nil {
		return
	}
	d.CancelAll()
	d.rebuildVisible()
	d.refreshRows()
}

func (d *Dashboard) inspectSelected() {
	d.inspectMsg = ""
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.Items) {
		return
	}
	st := d.Items[idx]
	if st.Header || st.Status != model.StatusFailed || st.Err == nil {
		return
	}
	d.inspectMsg = displayText(st.Err.Error())
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
	if d.searchQuery == "" || len(d.Items) == 0 {
		return
	}
	d.jumpIf(session.MatchTitle(d.searchQuery))
}

func (d *Dashboard) jumpIf(match func(session.Item) bool) {
	if len(d.visible) == 0 {
		return
	}
	if row := d.FindNext(d.visible, d.table.Cursor(), match); row >= 0 {
		d.selectRow(row)
	}
}

// retryAllFailed resets every failed item and starts fresh downloads for
// them, so a batch's failures can be retried without hunting for each row.
func (d *Dashboard) retryAllFailed() tea.Cmd {
	return d.startRetries(d.RetryAllFailed())
}

// retrySelected resets the selected failed item back to pending and
// kicks off a fresh download attempt.
func (d *Dashboard) retrySelected() tea.Cmd {
	return d.startRetries(d.Retry(d.stateAt(d.table.Cursor())))
}

func (d *Dashboard) startRetries(tasks []session.Task) tea.Cmd {
	if len(tasks) == 0 {
		return nil
	}
	d.rebuildVisible()
	d.refreshRows()
	return tea.Batch(d.runTasks(tasks), d.ensureRefreshTicker())
}

func (d *Dashboard) toggleFoldAtCursor() {
	if len(d.Items) == 0 {
		return
	}
	if d.ToggleFold(d.stateAt(d.table.Cursor())) {
		d.rebuildVisible()
		d.refreshRows()
		d.fitTableHeight()
	}
}

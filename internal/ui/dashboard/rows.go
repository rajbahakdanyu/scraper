package dashboard

import (
	"fmt"
	"slices"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func (d *Dashboard) refreshRows() {
	if !d.visibleReady || (len(d.visible) > 0 && d.visible[len(d.visible)-1] >= len(d.states)) {
		d.rebuildVisible()
	}
	if len(d.rows) != len(d.visible) {
		d.rows = make([]table.Row, len(d.visible))
	}
	// The table only renders rows within one viewport height of the cursor.
	cursor, reach := max(d.table.Cursor(), 0), d.table.Height()+1
	pending, next := 0, 0
	for row := max(cursor-reach, 0); row < min(cursor+reach, len(d.visible)); row++ {
		i := d.visible[row]
		for ; next <= i; next++ {
			if !d.states[next].isHeader && d.states[next].status == model.StatusPending {
				pending++
			}
		}
		st := d.states[i]
		if st.isHeader {
			var done, failed, total int
			for j := i + 1; j < len(d.states) && !d.states[j].isHeader; j++ {
				total++
				switch d.states[j].status {
				case model.StatusCompleted:
					done++
				case model.StatusFailed:
					failed++
				}
			}
			pct := 0
			if total > 0 {
				pct = 100 * done / total
			}
			chevron := "▾"
			if d.folded[i] {
				chevron = "▸"
			}
			headerText := fmt.Sprintf("%s %s  (%s)", chevron, displayText(st.albumName), displayText(st.albumSite))
			progressText := fmt.Sprintf("%s %d%%", renderBar(int64(done), int64(total), 10), pct)
			if failed > 0 {
				progressText += fmt.Sprintf(" (%d failed)", failed)
			}
			styled := d.theme.Cyan.Bold(true).Render(headerText)
			d.rows[row] = table.Row{"", styled, fmt.Sprintf("%d/%d", done, total), progressText}
			continue
		}

		mark := " "
		switch st.status {
		case model.StatusDownloading:
			mark = d.spinner.View()
		case model.StatusCompleted:
			mark = "✓"
		case model.StatusFailed:
			mark = "✗"
		case model.StatusCancelled:
			mark = "⊘"
		case model.StatusPending:
			mark = "·"
		}

		progressText := miniBar(st)
		if st.status == model.StatusDownloading && st.speed > 0 {
			progressText += fmt.Sprintf("  %s/s", HumanizeBytes(int64(st.speed)))
		}
		if st.status == model.StatusPending {
			progressText = fmt.Sprintf("queued #%d", pending)
		}
		d.rows[row] = table.Row{mark, truncate(displayText(st.item.Title), 32), statusShort(st.status), progressText}
	}
	d.table.SetRows(d.rows)
}

// ensureStateIndex indexes newly appended states incrementally. States are
// append-only, so rebuilding the whole map on every progress event (O(n) per
// event, O(n²) per run in batch mode) is unnecessary.
func (d *Dashboard) ensureStateIndex() {
	if d.stateIndex == nil {
		d.stateIndex = make(map[string]int)
	}
	for ; d.indexedStates < len(d.states); d.indexedStates++ {
		st := &d.states[d.indexedStates]
		if !st.isHeader {
			d.stateIndex[st.item.Key()] = d.indexedStates
		}
	}
}

func (d *Dashboard) rebuildVisible() {
	oldState := -1
	if len(d.visible) > 0 && d.table.Cursor() >= 0 && d.table.Cursor() < len(d.visible) && d.visible[d.table.Cursor()] < len(d.states) {
		oldState = d.visible[d.table.Cursor()]
	}
	if d.folded == nil {
		d.folded = make(map[int]bool)
	}
	if d.manuallyUnfolded == nil {
		d.manuallyUnfolded = make(map[int]bool)
	}
	match := func(st itemState) bool {
		switch d.filter {
		case filterFailed:
			return st.status == model.StatusFailed
		case filterActive:
			return st.status == model.StatusDownloading
		case filterPending:
			return st.status == model.StatusPending
		default:
			return true
		}
	}
	var visible []int
	for i := 0; i < len(d.states); i++ {
		st := d.states[i]
		if !st.isHeader {
			if match(st) {
				visible = append(visible, i)
			}
			continue
		}
		end := i + 1
		for end < len(d.states) && !d.states[end].isHeader {
			end++
		}
		children := make([]int, 0, end-i-1)
		for j := i + 1; j < end; j++ {
			if match(d.states[j]) {
				children = append(children, j)
			}
		}
		if len(children) > 0 {
			visible = append(visible, i)
			if !d.folded[i] {
				visible = append(visible, children...)
			}
		}
		i = end - 1
	}
	// Single-item runs have no album header and must remain visible.
	if len(d.states) > 0 && len(visible) == 0 && d.filter == filterAll {
		for i := range d.states {
			visible = append(visible, i)
		}
	}
	changed := !d.visibleReady || !slices.Equal(visible, d.visible)
	d.visible = visible
	d.visibleReady = true
	if !changed {
		return
	}
	d.refreshRows()
	if len(visible) > 0 {
		row := 0
		if oldState >= 0 {
			if r, ok := d.rowOfState(oldState); ok {
				row = r
			} else {
				for h := oldState; h >= 0; h-- {
					if d.states[h].isHeader {
						if r, ok := d.rowOfState(h); ok {
							row = r
						}
						break
					}
				}
			}
		}
		if row >= len(visible) {
			row = len(visible) - 1
		}
		if row != d.table.Cursor() {
			d.selectRow(row)
		}
	} else {
		d.table.SetCursor(0)
	}
}

func (d *Dashboard) skipHeader(row int) bool {
	idx := d.stateAt(row)
	return d.states[idx].isHeader && !d.folded[idx]
}

// selectRow jumps to row, scrolling it into view at the bottom edge.
func (d *Dashboard) selectRow(row int) {
	d.table.SetCursor(0)
	d.table.MoveUp(0)
	d.table.MoveDown(row)
}

// moveCursorTo steps to row so the table keeps its scroll position.
func (d *Dashboard) moveCursorTo(row int) {
	if delta := row - d.table.Cursor(); delta > 0 {
		d.table.MoveDown(delta)
	} else if delta < 0 {
		d.table.MoveUp(-delta)
	}
}

// syncTable refits the table and re-renders the rows it can show.
func (d *Dashboard) syncTable() {
	d.fitTableHeight()
	d.refreshRows()
}

func (d *Dashboard) stateAt(row int) int {
	if d.visibleReady && len(d.visible) == 0 {
		return -1
	}
	if !d.visibleReady {
		if row < 0 {
			return 0
		}
		if row >= len(d.states) {
			return len(d.states) - 1
		}
		return row
	}
	if row < 0 {
		row = 0
	}
	if row >= len(d.visible) {
		row = len(d.visible) - 1
	}
	return d.visible[row]
}

func (d *Dashboard) rowOfState(stateIdx int) (int, bool) {
	for row, idx := range d.visible {
		if idx == stateIdx {
			return row, true
		}
	}
	return 0, false
}

// tableChrome returns the terminal rows the batch view spends on
// everything around the table in the current state: header, stats, panel
// borders, footer, and any open status/error/search sections. The table
// gets whatever height is left.
func (d *Dashboard) tableChrome() int {
	chrome := 9 // leading blank, header, bar, stats, blank, panel border(2), trailing blank, footer
	if d.status != "" {
		chrome += 2
	}
	if d.inspectMsg != "" {
		chrome += 2
	}
	if d.showErrors && len(d.errorLog) > 0 {
		chrome += 1 + lipgloss.Height(d.viewErrorPanel())
	}
	if d.searching || d.searchQuery != "" {
		chrome += 2
	}
	return chrome
}

// fitTableHeight gives the table the terminal's leftover height. The
// dashboard previously capped the table at 15 rows, so a fullscreen
// terminal still scrolled; a WindowSizeMsg and every scrape refresh
// re-fit instead.
func (d *Dashboard) fitTableHeight() {
	if !d.ready || d.height <= 0 {
		return
	}
	h := d.height - d.tableChrome()
	if h < 4 {
		h = 4
	}
	if rows := len(d.visible) + 1; rows > 1 && h > rows {
		h = rows // column header plus every row: no dead space below a short list
	}
	before := d.table.Height()
	d.table.SetHeight(h)
	if d.table.Height() != before {
		d.selectRow(d.table.Cursor())
	}
}

package dashboard

import (
	"fmt"
	"slices"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func (d *Dashboard) refreshRows() {
	if !d.visibleReady || (len(d.visible) > 0 && d.visible[len(d.visible)-1] >= len(d.Items)) {
		d.rebuildVisible()
	}
	if len(d.rows) != len(d.visible) {
		d.rows = make([]table.Row, len(d.visible))
	}
	// The table only renders rows within one viewport height of the cursor.
	cursor, reach := max(d.table.Cursor(), 0), d.table.Height()+1
	from := max(cursor-reach, 0)
	for n, r := range d.Rows(d.visible, from, cursor+reach) {
		row := from + n
		st := r.State
		if st.Header {
			pct := 0
			if r.Count > 0 {
				pct = 100 * r.Done / r.Count
			}
			chevron := "▾"
			if r.Folded {
				chevron = "▸"
			}
			headerText := fmt.Sprintf("%s %s  (%s)", chevron, displayText(st.Album), displayText(st.Site))
			progressText := fmt.Sprintf("%s %d%%", renderBar(int64(r.Done), int64(r.Count), 10), pct)
			if r.Failed > 0 {
				progressText += fmt.Sprintf(" (%d failed)", r.Failed)
			}
			styled := d.theme.Cyan.Bold(true).Render(headerText)
			d.rows[row] = table.Row{"", styled, fmt.Sprintf("%d/%d", r.Done, r.Count), progressText}
			continue
		}

		mark := " "
		switch st.Status {
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
		if st.Status == model.StatusDownloading && st.Speed > 0 {
			progressText += fmt.Sprintf("  %s/s", fileutil.HumanBytes(int64(st.Speed)))
		}
		if st.Status == model.StatusPending {
			progressText = fmt.Sprintf("queued #%d", r.QueuePos)
		}
		d.rows[row] = table.Row{mark, truncate(displayText(st.Item.Title), 32), statusShort(st.Status), progressText}
	}
	d.table.SetRows(d.rows)
}

func (d *Dashboard) rebuildVisible() {
	oldState := -1
	if len(d.visible) > 0 && d.table.Cursor() >= 0 && d.table.Cursor() < len(d.visible) && d.visible[d.table.Cursor()] < len(d.Items) {
		oldState = d.visible[d.table.Cursor()]
	}
	visible := d.Visible()
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
			} else if h := d.HeaderOf(oldState); h >= 0 {
				if r, ok := d.rowOfState(h); ok {
					row = r
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
	return d.Items[idx].Header && !d.Folded[idx]
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
		if row >= len(d.Items) {
			return len(d.Items) - 1
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
	if d.Status != "" {
		chrome += 2
	}
	if d.inspectMsg != "" {
		chrome += 2
	}
	if d.showErrors && len(d.ErrorLog) > 0 {
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

package dashboard

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/session"
)

func TestBatchHeaderRowsCreated(t *testing.T) {
	urls := []model.Album{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/test", Slug: "test", Dest: "/tmp/test"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}
	msg := batchScrapeMsg{album: urls[0], idx: 0, items: items}
	_, cmd := d.handleBatchScrape(msg)
	if cmd == nil {
		t.Fatal("expected command after first batch scrape")
	}

	if len(d.Items) == 0 {
		t.Fatal("expected states to be populated")
	}
	if !d.Items[0].Header {
		t.Error("expected first state to be a header row")
	}
	if d.Items[0].Album != "test" {
		t.Errorf("expected header album name 'test', got %q", d.Items[0].Album)
	}
}

func TestBatchHeaderCursorSkip(t *testing.T) {
	urls := []model.Album{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/one", Slug: "one", Dest: t.TempDir()},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/two", Slug: "two", Dest: t.TempDir()},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, t.TempDir(), urls)

	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}
	_, _ = d.handleBatchScrape(batchScrapeMsg{album: urls[0], idx: 0, items: items})
	_, _ = d.handleBatchScrape(batchScrapeMsg{album: urls[1], idx: 1, items: items})

	d.table.SetHeight(10)
	d.refreshRows()

	if !d.Items[0].Header {
		t.Fatal("expected a header at row 0")
	}

	// Moving up from the first item must never park on row 0's header.
	d.table.SetCursor(1)
	d.moveUpSkippingHeaders()
	if idx := d.table.Cursor(); idx < 0 || idx >= len(d.Items) || d.Items[idx].Header {
		t.Fatalf("moveUp parked on header at index %d", idx)
	}

	// Moving down from a header between albums must land on the next item.
	secondHeader := -1
	for i := 1; i < len(d.Items); i++ {
		if d.Items[i].Header {
			secondHeader = i
			break
		}
	}
	if secondHeader == -1 {
		t.Fatal("expected a second album header")
	}
	d.table.SetCursor(secondHeader)
	d.moveDownSkippingHeaders()
	if idx := d.table.Cursor(); d.Items[idx].Header {
		t.Fatalf("moveDown parked on header at index %d", idx)
	}
}

func TestHeaderRowRenderedInVisibleColumn(t *testing.T) {
	urls := []model.Album{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/test", Slug: "test", Dest: "/tmp/test"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	msg := batchScrapeMsg{album: urls[0], idx: 0, items: items}
	d.handleBatchScrape(msg)
	d.refreshRows()

	// Header row (index 0) must have its styled text in column 1 (FILE column),
	// not column 0 (the 2-char-wide mark column) where it would be invisible.
	if d.rows[0][0] != "" {
		t.Errorf("header row column 0 should be empty, got %q", d.rows[0][0])
	}
	if d.rows[0][1] == "" {
		t.Error("header row column 1 must contain the styled header text")
	}
}

func TestFitTableHeightFillsTerminal(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	for i := 0; i < 50; i++ {
		d.Items = append(d.Items, session.Item{Item: model.MediaItem{Title: fmt.Sprintf("f%d.mp4", i)}})
	}
	d.refreshRows()
	d.ready = true
	d.height = 40
	d.width = 120

	d.fitTableHeight()

	if got := lines(d.table.View()); got != 31 {
		t.Fatalf("table renders %d lines, want 31 (terminal 40 minus 9 chrome)", got)
	}
	d.Phase = session.PhaseDownload // the table only shows in the batch view
	if got := lines(d.View()); got != d.height {
		t.Fatalf("dashboard renders %d lines in a %d-line terminal: it must fill the terminal exactly", got, d.height)
	}
}

func TestFitTableHeightCapsAtRowCount(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Item: model.MediaItem{Title: "a.mp4"}},
		{Item: model.MediaItem{Title: "b.mp4"}},
	}
	d.refreshRows()
	d.ready = true
	d.height = 40
	d.width = 120

	d.fitTableHeight()

	if got := lines(d.table.View()); got != 3 {
		t.Fatalf("table renders %d lines, want 3 (column header plus both rows, no dead space)", got)
	}
}

func TestFitTableHeightFloorsAtMinimum(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	for i := 0; i < 50; i++ {
		d.Items = append(d.Items, session.Item{Item: model.MediaItem{Title: fmt.Sprintf("f%d.mp4", i)}})
	}
	d.refreshRows()
	d.ready = true
	d.height = 8 // 8 - 10 chrome is negative; floor at a usable 4
	d.width = 120

	d.fitTableHeight()

	if got := lines(d.table.View()); got != 4 {
		t.Fatalf("table renders %d lines, want 4 (minimum in a tiny terminal)", got)
	}
}

func TestWindowSizeMsgRefitsTable(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	for i := 0; i < 50; i++ {
		d.Items = append(d.Items, session.Item{Item: model.MediaItem{Title: fmt.Sprintf("f%d.mp4", i)}})
	}
	d.refreshRows()

	updated, _ := d.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	got := updated.(*Dashboard)
	if h := lines(got.table.View()); h != 31 {
		t.Fatalf("after resize, table renders %d lines, want 31", h)
	}
}

func TestVisibleMappingPreservesSelectionWhenGrowing(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true, Album: "first"},
		{Item: model.MediaItem{Title: "completed"}, Status: model.StatusCompleted},
		{Header: true, Album: "second"},
		{Item: model.MediaItem{Title: "failed"}, Status: model.StatusFailed},
		{Header: true, Album: "third"},
		{Item: model.MediaItem{Title: "completed"}, Status: model.StatusCompleted},
		{Header: true, Album: "last"},
		{Item: model.MediaItem{Title: "selected"}, Status: model.StatusPending},
	}
	d.Filter = session.FilterPending
	d.refreshRows()
	d.table.SetCursor(1)
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if got := d.stateAt(d.table.Cursor()); got != 7 {
		t.Fatalf("selected state = %d, want 7 after growing the visible list", got)
	}
}

func TestFilterCycleTracksLiveStatesAndGlobalTotals(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true, Album: "mixed"},
		{Item: model.MediaItem{URL: "https://example.com/one", Title: "completed", Size: 100}, Status: model.StatusCompleted},
		{Item: model.MediaItem{URL: "https://example.com/two", Title: "failed", Size: 100}, Status: model.StatusFailed},
		{Item: model.MediaItem{URL: "https://example.com/three", Title: "active", Size: 100}, Status: model.StatusDownloading, Bytes: 50},
		{Header: true, Album: "queued"},
		{Item: model.MediaItem{URL: "https://example.com/four", Title: "pending", Size: 100}, Status: model.StatusPending},
	}
	d.refreshRows()
	for _, test := range []struct {
		filter session.Filter
		rows   []int
	}{
		{session.FilterFailed, []int{0, 2}},
		{session.FilterActive, []int{0, 3}},
		{session.FilterPending, []int{4, 5}},
		{session.FilterAll, []int{0, 1, 2, 3, 4, 5}},
	} {
		d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if d.Filter != test.filter || !slices.Equal(d.visible, test.rows) {
			t.Fatalf("filter %d: visible = %v, want %v", d.Filter, d.visible, test.rows)
		}
		if done, total, _ := d.ProgressSums(); done != 150 || total != 400 {
			t.Fatalf("filter changed global byte totals: %d/%d", done, total)
		}
	}
	d.Filter = session.FilterActive
	d.rebuildVisible()
	d.Update(progressMsg(model.ProgressEvent{Item: d.Items[5].Item, Status: model.StatusDownloading, Bytes: 25, Total: 100}))
	if !slices.Equal(d.visible, []int{0, 3, 4, 5}) {
		t.Fatalf("active filter did not track live progress: %v", d.visible)
	}
	d.Update(downloadDoneMsg{key: d.Items[3].Item.Key()})
	if !slices.Equal(d.visible, []int{4, 5}) {
		t.Fatalf("active filter retained a completed Item: %v", d.visible)
	}
}

func TestFoldedHeadersRemainReachable(t *testing.T) {
	for _, wheel := range []bool{false, true} {
		t.Run(fmt.Sprintf("wheel-%v", wheel), func(t *testing.T) {
			d := NewBatch(nil, t.TempDir(), nil)
			d.Items = []session.Item{
				{Header: true, Album: "first"},
				{Item: model.MediaItem{Title: "one"}, Status: model.StatusPending},
				{Header: true, Album: "second"},
				{Item: model.MediaItem{Title: "two"}, Status: model.StatusPending},
				{Header: true, Album: "third"},
				{Item: model.MediaItem{Title: "three"}, Status: model.StatusPending},
			}
			d.Folded = map[int]bool{2: true, 4: true}
			d.refreshRows()
			d.table.SetCursor(1)
			var down, up tea.Msg = tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyUp}
			if wheel {
				down = tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}
				up = tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
			}
			for _, want := range []int{2, 4} {
				d.Update(down)
				if got := d.stateAt(d.table.Cursor()); got != want {
					t.Fatalf("down selects state %d, want folded header %d", got, want)
				}
			}
			d.Update(up)
			if got := d.stateAt(d.table.Cursor()); got != 2 {
				t.Fatalf("up selects state %d, want folded header 2", got)
			}
			d.Update(tea.KeyMsg{Type: tea.KeyTab})
			if _, visible := d.rowOfState(3); !visible {
				t.Fatal("Tab did not unfold the selected album")
			}
		})
	}
}

func TestEmptyFilterCannotSelectHiddenItem(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	d := NewBatch(fd, t.TempDir(), nil)
	d.Items = []session.Item{{Item: model.MediaItem{URL: "https://x/1", Title: "done"}, Status: model.StatusCompleted}}
	d.refreshRows()
	d.Filter = session.FilterFailed
	d.rebuildVisible()
	if len(d.visible) != 0 || d.stateAt(0) != -1 {
		t.Fatalf("empty filtered view maps to a hidden state: %v, stateAt=%d", d.visible, d.stateAt(0))
	}
	d.cancelSelected()
	if len(fd.cancelled) != 0 {
		t.Fatalf("cancel selected affected hidden item")
	}
}

func TestRefreshTickCostGrowsLinearly(t *testing.T) {
	cost := func(n int) time.Duration {
		d := NewBatch(nil, t.TempDir(), nil)
		d.Items = append(d.Items, session.Item{Header: true, Album: "album"})
		for index := 0; index < n; index++ {
			d.Items = append(d.Items, session.Item{Item: model.MediaItem{URL: fmt.Sprintf("https://example.com/%d", index), Title: "file"}, Status: model.StatusPending})
		}
		d.refreshRows()
		d.Phase = session.PhaseDownload
		best := time.Hour
		for range 3 {
			start := time.Now()
			d.Update(refreshTickMsg{})
			best = min(best, time.Since(start))
		}
		return best
	}
	small, large := cost(1000), cost(20000)
	// 20x the items: linear work is ~20x, quadratic work is ~400x.
	if large > 60*small {
		t.Fatalf("refresh tick took %v for 1k items but %v for 20k items", small, large)
	}
}

func TestScrollPositionStableAcrossNavigationAndTicks(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), nil)
	for index := 0; index < 60; index++ {
		d.Items = append(d.Items, session.Item{Item: model.MediaItem{URL: fmt.Sprintf("https://example.com/%d", index), Title: fmt.Sprintf("item-%02d", index)}, Status: model.StatusPending})
	}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for range 40 {
		d.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	for range 3 {
		d.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	view := d.table.View()
	selected := fmt.Sprintf("item-%02d", d.stateAt(d.table.Cursor()))
	rendered := strings.Split(view, "\n")
	if !strings.Contains(view, selected) {
		t.Fatalf("selected %s is not visible:\n%s", selected, view)
	}
	if strings.Contains(rendered[len(rendered)-1], selected) {
		t.Fatalf("moving up scrolled the list instead of moving the selection:\n%s", view)
	}
	d.Update(refreshTickMsg{})
	d.Update(spinner.TickMsg{})
	if got := d.table.View(); got != view {
		t.Fatalf("a refresh moved the scroll position:\nbefore:\n%s\nafter:\n%s", view, got)
	}
}

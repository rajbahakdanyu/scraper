package dashboard

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestSpinnerStopsAfterBatchCompletes(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	if !d.spinnerActive() {
		t.Fatal("expected spinner active while a download is in progress")
	}
	if _, cmd := d.Update(spinner.TickMsg{}); cmd == nil {
		t.Error("expected spinner tick to be re-armed while active")
	}

	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key()})
	if d.spinnerActive() {
		t.Error("expected spinner inactive once all items are done")
	}
	if _, cmd := d.Update(spinner.TickMsg{}); cmd != nil {
		t.Error("expected spinner tick to stop once all items are done")
	}
}

func TestRenderStatsShowsStaticIconWhenDone(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key()})

	if stats := d.renderStats(); !strings.Contains(stats, "✓") {
		t.Errorf("expected a static checkmark once all downloads finish, got: %q", stats)
	}
}

func TestRenderStatsShowsXWhenFailed(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key(), err: errors.New("boom")})

	if stats := d.renderStats(); !strings.Contains(stats, "✗") {
		t.Errorf("expected a static X once a download fails, got: %q", stats)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"short string unchanged", "abc", 10, "abc"},
		{"exact length unchanged", "abcde", 5, "abcde"},
		{"ascii truncation", "abcdefghij", 5, "abcd…"},
		// Multi-byte runes near the cutoff must not be split, which a
		// byte-index slice (s[:max-1]) would do and produce a garbled or
		// invalid UTF-8 string.
		{"multi-byte runes truncated safely", "日本語のタイトルです", 5, "日本語の…"},
		{"emoji truncated safely", "🎬🎬🎬🎬🎬🎬🎬", 3, "🎬🎬…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%q, %d) produced invalid UTF-8: %q", tt.s, tt.max, got)
			}
		})
	}
}

func TestBatchModeStatsShowsScraping(t *testing.T) {
	urls := []model.Album{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/test", Slug: "test", Dest: "/tmp/test"},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/test2", Slug: "test2", Dest: "/tmp/test2"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	msg := batchScrapeMsg{album: urls[0], idx: 0, items: items}
	d.handleBatchScrape(msg)

	stats := d.renderStats()
	if !strings.Contains(stats, "scraping") {
		t.Errorf("stats should contain 'scraping' while batchRemaining > 0, got: %q", stats)
	}
}

func TestBatchModeSpinnerActiveWhileScraping(t *testing.T) {
	urls := []model.Album{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/1", Slug: "one", Dest: "/tmp/one"},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/2", Slug: "two", Dest: "/tmp/two"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	// Feed first album
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	msg := batchScrapeMsg{album: urls[0], idx: 0, items: items}
	d.handleBatchScrape(msg)

	// First album downloads done, but second still scraping
	for i := range d.states {
		if !d.states[i].isHeader {
			d.states[i].status = model.StatusCompleted
		}
	}

	// batchRemaining should still be 1, spinner stays active
	if d.batchRemaining != 1 {
		t.Errorf("expected batchRemaining = 1, got %d", d.batchRemaining)
	}
	if !d.spinnerActive() {
		t.Error("spinner should be active while batchRemaining > 0 even if all current downloads done")
	}
}

func TestViewRefitsForTransientSections(t *testing.T) {
	for _, errorCount := range []int{1, 2, 8, 10} {
		t.Run(fmt.Sprintf("errors-%d", errorCount), func(t *testing.T) {
			d := NewBatch(nil, t.TempDir(), nil)
			for index := 0; index < 50; index++ {
				d.states = append(d.states, itemState{item: model.MediaItem{URL: fmt.Sprintf("https://example.com/%d", index), Title: "file.mp4"}, status: model.StatusPending})
			}
			d.refreshRows()
			d.phase = phaseDownload
			d.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
			for index := 0; index < errorCount; index++ {
				d.appendError("scrape failed")
			}
			d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
			d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			d.Update(downloadDoneMsg{key: d.states[0].item.Key(), err: errors.New("connection reset")})
			if got := lines(d.View()); got != d.height {
				t.Fatalf("busy view renders %d lines, want %d", got, d.height)
			}
			d.Update(statusClearMsg{seq: d.statusSeq})
			d.Update(tea.KeyMsg{Type: tea.KeyEsc})
			d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
			if got := lines(d.View()); got != d.height {
				t.Fatalf("cleared view renders %d lines, want %d", got, d.height)
			}
		})
	}
}

func TestViewFitsTerminalDimensions(t *testing.T) {
	for _, width := range []int{80, 120} {
		for _, runPhase := range []phase{phaseLoading, phaseDownload, phaseDone, phaseSummary} {
			t.Run(fmt.Sprintf("width-%d-phase-%d", width, runPhase), func(t *testing.T) {
				d := NewBatch(nil, strings.Repeat("/downloads", 20), nil)
				d.states = []itemState{{isHeader: true, albumName: "a-very-long-album-name", albumSite: "bunkr"}}
				for index := 0; index < 50; index++ {
					d.states = append(d.states, itemState{item: model.MediaItem{Title: "file.mp4", Size: 1000}, status: model.StatusDownloading, total: 1000, bytes: 500, speed: 1024 * 1024})
				}
				d.states[1].status = model.StatusCompleted
				d.states[2].status = model.StatusFailed
				d.refreshRows()
				d.phase = runPhase
				if runPhase == phaseDone {
					d.states = nil
					d.loadingErr = strings.Repeat("long error ", 20)
				}
				d.setStatus("%s", strings.Repeat("status\n", 30))
				d.inspectMsg = strings.Repeat("inspect\n", 30)
				d.searching, d.searchQuery = true, strings.Repeat("search", 30)
				d.showErrors = true
				for index := 0; index < 10; index++ {
					d.appendError("an error\nwith detail " + strings.Repeat("long", 40))
				}
				d.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				out := d.View()
				for _, line := range strings.Split(out, "\n") {
					if got := lipgloss.Width(line); got > width {
						t.Fatalf("rendered line width = %d, terminal width = %d: %q", got, width, line)
					}
				}
				if got := lines(out); got > d.height {
					t.Fatalf("view renders %d lines in a %d-line terminal", got, d.height)
				}
				if runPhase == phaseDownload {
					for _, text := range []string{"1/50", "(1 failed)", "▓"} {
						if !strings.Contains(out, text) {
							t.Fatalf("row information %q missing from rendered view:\n%s", text, out)
						}
					}
					d.Update(tea.KeyMsg{Type: tea.KeyEsc})
					for step := 0; step < 3; step++ {
						d.Update(tea.KeyMsg{Type: tea.KeyDown})
					}
					out = d.View()
					for _, text := range []string{"50%", "1.0M/s"} {
						if !strings.Contains(out, text) {
							t.Fatalf("active row information %q missing from rendered view:\n%s", text, out)
						}
					}
				}
			})
		}
	}
}

func TestOverallBarBoundsAndUnknownSizes(t *testing.T) {
	for _, test := range []struct {
		done, total int64
		width       int
		want        string
	}{
		{50, 100, 10, "▓▓▓▓▓░░░░░"},
		{0, 0, 4, "░░░░"},
		{120, 100, 4, "▓▓▓▓"},
		{-10, 100, 4, "░░░░"},
		{50, 100, 0, ""},
	} {
		if got := renderBar(test.done, test.total, test.width); got != test.want {
			t.Fatalf("renderBar(%d, %d, %d) = %q, want %q", test.done, test.total, test.width, got, test.want)
		}
	}
	d := NewBatch(nil, t.TempDir(), nil)
	d.states = []itemState{
		{isHeader: true, total: 999},
		{item: model.MediaItem{Title: "completed", Size: 100}, status: model.StatusCompleted},
		{item: model.MediaItem{Title: "active"}, total: 100, bytes: 50, status: model.StatusDownloading},
		{item: model.MediaItem{Title: "unknown"}, status: model.StatusPending},
	}
	d.refreshRows()
	d.phase = phaseDownload
	d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if done, total, unknown := d.runProgressSums(); done != 150 || total != 200 || unknown != 1 {
		t.Fatalf("run byte totals = %d/%d, unknown %d", done, total, unknown)
	}
	if !strings.Contains(d.View(), "75%~") {
		t.Fatal("unknown-size backlog must mark the percentage as approximate")
	}
}

func TestFailureStatusSequenceAndSummarySettle(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.states = []itemState{{item: model.MediaItem{URL: "https://x/1", Title: "gone.mp4"}, status: model.StatusDownloading}}
	d.refreshRows()
	updated, _ := d.Update(downloadDoneMsg{key: d.states[0].item.Key(), err: errors.New("connection reset")})
	d = updated.(*Dashboard)
	if !strings.Contains(d.status, "gone.mp4") || !strings.Contains(d.status, "connection reset") {
		t.Fatalf("failure status = %q", d.status)
	}
	oldSeq := d.statusSeq
	updated, _ = d.Update(downloadDoneMsg{key: d.states[0].item.Key(), err: errors.New("new error")})
	d = updated.(*Dashboard)
	updated, _ = d.Update(statusClearMsg{seq: oldSeq})
	d = updated.(*Dashboard)
	if !strings.Contains(d.status, "new error") {
		t.Fatalf("stale clear erased newer status: %q", d.status)
	}

	d.phase = phaseDownload
	updated, _ = d.Update(refreshTickMsg{})
	d = updated.(*Dashboard)
	if d.phase != phaseSummary || !strings.Contains(d.View(), "1 failed") {
		t.Fatalf("summary missing after settled run: phase=%v view=%q", d.phase, d.View())
	}
}

func TestSummaryRetryListWriterIsShown(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.states = []itemState{{item: model.MediaItem{URL: "https://x/1", Title: "gone"}, status: model.StatusFailed, lastErr: errors.New("boom")}}
	d.refreshRows()
	d.phase = phaseSummary
	d.SetRetryWriter(func([]model.FailedItem) (string, error) { return "/tmp/retry-list.txt", nil })
	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	d = updated.(*Dashboard)
	if !strings.Contains(d.View(), "/tmp/retry-list.txt") {
		t.Fatalf("summary did not show retry list result: %q", d.View())
	}
}

func TestSummaryWaitsForFinalDownloadResult(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.states = []itemState{{item: model.MediaItem{URL: "https://example.com/one", Title: "one.mp4", Size: 100}, status: model.StatusDownloading}}
	d.refreshRows()
	d.phase = phaseDownload
	d.Update(progressMsg(model.ProgressEvent{Item: d.states[0].item, Bytes: 100, Total: 100, Status: model.StatusCompleted}))
	d.Update(refreshTickMsg{})
	if d.phase != phaseDownload {
		t.Fatal("summary appeared before the final download result")
	}
	d.Update(downloadDoneMsg{key: d.states[0].item.Key(), err: errors.New("close failed")})
	d.Update(refreshTickMsg{})
	if d.phase != phaseSummary || len(d.Failures()) != 1 {
		t.Fatal("summary must use the authoritative final download failure")
	}
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if d.phase != phaseDownload || d.states[0].status != model.StatusPending {
		t.Fatal("retry must return to the download table with pending work")
	}
}

func TestSummaryWaitsForRemainingAlbums(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), []model.Album{{Site: "test"}})
	d.states = []itemState{{item: model.MediaItem{Title: "one"}, status: model.StatusCompleted}}
	d.refreshRows()
	d.phase = phaseDownload
	d.Update(refreshTickMsg{})
	if d.phase != phaseDownload {
		t.Fatal("summary appeared while another album is still being scraped")
	}
}

func TestUntrustedTextCannotEmitTerminalControls(t *testing.T) {
	evil := "x\x1b]52;c;ZXZpbA==\x07\x1b[2J\u009b31m\u202e"
	d := NewBatch(nil, t.TempDir(), nil)
	d.states = []itemState{
		{isHeader: true, albumName: "album" + evil, albumSite: "site" + evil},
		{item: model.MediaItem{URL: "https://example.com/1", Title: "title" + evil}, status: model.StatusDownloading},
	}
	d.refreshRows()
	d.phase = phaseDownload
	d.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	d.appendError("scrape" + evil)
	d.showErrors = true
	d.Update(downloadDoneMsg{key: d.states[1].item.Key(), err: errors.New("error" + evil)})
	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	d.searchQuery = "search" + evil
	views := []string{d.View()}
	d.phase, d.states, d.loadingErr = phaseDone, nil, "load"+evil
	views = append(views, d.View())
	for _, out := range views {
		for _, control := range []string{"\x1b", "\x07", "\u009b", "\u202e"} {
			if strings.Contains(out, control) {
				t.Fatalf("view contains control %q:\n%q", control, out)
			}
		}
	}
	for _, text := range []string{"titlex]52;c;", "albumx", "errorx", "scrapex", "searchx"} {
		if !strings.Contains(views[0], text) {
			t.Fatalf("sanitized text %q missing from view:\n%s", text, views[0])
		}
	}
}

func TestViewPreservesInternalWhitespace(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), nil)
	d.states = []itemState{{item: model.MediaItem{URL: "https://example.com/1", Title: "file"}, status: model.StatusPending}}
	d.refreshRows()
	d.phase = phaseDownload
	d.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	d.setStatus("two  spaces")
	out := d.View()
	for _, text := range []string{"◢◤  SCRAPER  ◢◤", "two  spaces"} {
		if !strings.Contains(out, text) {
			t.Fatalf("view collapsed whitespace in %q:\n%s", text, out)
		}
	}
}

func TestStatsIncludeETA(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.states = []itemState{
		{item: model.MediaItem{Title: "a.mp4", Size: 1000}, status: model.StatusDownloading, total: 1000, bytes: 500, speed: 100},
	}
	if !strings.Contains(d.renderStats(), "eta ") {
		t.Fatalf("stats should include an ETA: %s", d.renderStats())
	}
}

func TestStatsHideETAWhenSizesUnknown(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.states = []itemState{
		{item: model.MediaItem{Title: "a.mp4", Size: 1000}, status: model.StatusDownloading, total: 1000, bytes: 500, speed: 100},
		{item: model.MediaItem{Title: "b.mp4"}, status: model.StatusPending},
	}
	if strings.Contains(d.renderStats(), "eta ") {
		t.Fatal("ETA must be hidden when queued items have unknown size")
	}
}

func TestErrorPanelToggles(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.phase = phaseDownload
	d.appendError("https://bunkr.cr/a/broken: no file links")

	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got, ok := updated.(*Dashboard)
	if !ok {
		t.Fatal("Update did not return *Dashboard")
	}
	if !got.showErrors {
		t.Fatal("pressing e should show the error panel")
	}
	if view := got.viewBatch("hdr"); !strings.Contains(view, "no file links") {
		t.Fatalf("error panel missing the recorded error:\n%s", view)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	hidden, ok := updated.(*Dashboard)
	if !ok {
		t.Fatal("Update did not return *Dashboard")
	}
	if hidden.showErrors {
		t.Fatal("pressing e again should hide the panel")
	}
	if view := hidden.viewBatch("hdr"); strings.Contains(view, "Errors (") {
		t.Fatalf("hidden panel still rendered:\n%s", view)
	}
}

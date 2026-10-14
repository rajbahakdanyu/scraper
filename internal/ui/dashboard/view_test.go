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
	"github.com/rajbahakdanyu/scraper/internal/session"
)

func TestSpinnerStopsAfterBatchCompletes(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	if !d.Active() {
		t.Fatal("expected spinner active while a download is in progress")
	}
	if _, cmd := d.Update(spinner.TickMsg{}); cmd == nil {
		t.Error("expected spinner tick to be re-armed while active")
	}

	d.applyDone(downloadDoneMsg{key: d.Items[0].Item.Key()})
	if d.Active() {
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
	d.applyDone(downloadDoneMsg{key: d.Items[0].Item.Key()})

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
	d.applyDone(downloadDoneMsg{key: d.Items[0].Item.Key(), err: errors.New("boom")})

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
	for i := range d.Items {
		if !d.Items[i].Header {
			d.Items[i].Status = model.StatusCompleted
		}
	}

	// batchRemaining should still be 1, spinner stays active
	if d.BatchRemaining != 1 {
		t.Errorf("expected batchRemaining = 1, got %d", d.BatchRemaining)
	}
	if !d.Active() {
		t.Error("spinner should be active while batchRemaining > 0 even if all current downloads done")
	}
}

func TestViewRefitsForTransientSections(t *testing.T) {
	for _, errorCount := range []int{1, 2, 8, 10} {
		t.Run(fmt.Sprintf("errors-%d", errorCount), func(t *testing.T) {
			d := NewBatch(nil, t.TempDir(), nil)
			for index := 0; index < 50; index++ {
				d.Items = append(d.Items, session.Item{Item: model.MediaItem{URL: fmt.Sprintf("https://example.com/%d", index), Title: "file.mp4"}, Status: model.StatusPending})
			}
			d.refreshRows()
			d.Phase = session.PhaseDownload
			d.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
			for index := 0; index < errorCount; index++ {
				d.AppendError("scrape failed")
			}
			d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
			d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			d.Update(downloadDoneMsg{key: d.Items[0].Item.Key(), err: errors.New("connection reset")})
			if got := lines(d.View()); got != d.height {
				t.Fatalf("busy view renders %d lines, want %d", got, d.height)
			}
			d.Update(statusClearMsg{seq: d.StatusSeq})
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
		for _, runPhase := range []session.Phase{session.PhaseLoading, session.PhaseDownload, session.PhaseDone, session.PhaseSummary} {
			t.Run(fmt.Sprintf("width-%d-phase-%d", width, runPhase), func(t *testing.T) {
				d := NewBatch(nil, strings.Repeat("/downloads", 20), nil)
				d.Items = []session.Item{{Header: true, Album: "a-very-long-album-name", Site: "bunkr"}}
				for index := 0; index < 50; index++ {
					d.Items = append(d.Items, session.Item{Item: model.MediaItem{Title: "file.mp4", Size: 1000}, Status: model.StatusDownloading, Total: 1000, Bytes: 500, Speed: 1024 * 1024})
				}
				d.Items[1].Status = model.StatusCompleted
				d.Items[2].Status = model.StatusFailed
				d.refreshRows()
				d.Phase = runPhase
				if runPhase == session.PhaseDone {
					d.Items = nil
					d.LoadingErr = strings.Repeat("long error ", 20)
				}
				d.SetStatus("%s", strings.Repeat("status\n", 30))
				d.inspectMsg = strings.Repeat("inspect\n", 30)
				d.searching, d.searchQuery = true, strings.Repeat("search", 30)
				d.showErrors = true
				for index := 0; index < 10; index++ {
					d.AppendError("an error\nwith detail " + strings.Repeat("long", 40))
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
				if runPhase == session.PhaseDownload {
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
	d.Items = []session.Item{
		{Header: true, Total: 999},
		{Item: model.MediaItem{Title: "completed", Size: 100}, Status: model.StatusCompleted},
		{Item: model.MediaItem{Title: "active"}, Total: 100, Bytes: 50, Status: model.StatusDownloading},
		{Item: model.MediaItem{Title: "unknown"}, Status: model.StatusPending},
	}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if done, total, unknown := d.ProgressSums(); done != 150 || total != 200 || unknown != 1 {
		t.Fatalf("run byte totals = %d/%d, unknown %d", done, total, unknown)
	}
	if !strings.Contains(d.View(), "75%~") {
		t.Fatal("unknown-size backlog must mark the percentage as approximate")
	}
}

func TestFailureStatusSequenceAndSummarySettle(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{{Item: model.MediaItem{URL: "https://x/1", Title: "gone.mp4"}, Status: model.StatusDownloading}}
	d.refreshRows()
	updated, _ := d.Update(downloadDoneMsg{key: d.Items[0].Item.Key(), err: errors.New("connection reset")})
	d = updated.(*Dashboard)
	if !strings.Contains(d.Status, "gone.mp4") || !strings.Contains(d.Status, "connection reset") {
		t.Fatalf("failure status = %q", d.Status)
	}
	oldSeq := d.StatusSeq
	updated, _ = d.Update(downloadDoneMsg{key: d.Items[0].Item.Key(), err: errors.New("new error")})
	d = updated.(*Dashboard)
	updated, _ = d.Update(statusClearMsg{seq: oldSeq})
	d = updated.(*Dashboard)
	if !strings.Contains(d.Status, "new error") {
		t.Fatalf("stale clear erased newer Status: %q", d.Status)
	}

	d.Phase = session.PhaseDownload
	updated, _ = d.Update(refreshTickMsg{})
	d = updated.(*Dashboard)
	if d.Phase != session.PhaseSummary || !strings.Contains(d.View(), "1 failed") {
		t.Fatalf("summary missing after settled run: phase=%v view=%q", d.Phase, d.View())
	}
}

func TestSummaryRetryListWriterIsShown(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{{Item: model.MediaItem{URL: "https://x/1", Title: "gone"}, Status: model.StatusFailed, Err: errors.New("boom")}}
	d.refreshRows()
	d.Phase = session.PhaseSummary
	d.SetRetryWriter(func([]model.FailedItem) (string, error) { return "/tmp/retry-list.txt", nil })
	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	d = updated.(*Dashboard)
	if !strings.Contains(d.View(), "/tmp/retry-list.txt") {
		t.Fatalf("summary did not show retry list result: %q", d.View())
	}
}

func TestSummaryWaitsForFinalDownloadResult(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{{Item: model.MediaItem{URL: "https://example.com/one", Title: "one.mp4", Size: 100}, Status: model.StatusDownloading}}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.Update(progressMsg(model.ProgressEvent{Item: d.Items[0].Item, Bytes: 100, Total: 100, Status: model.StatusCompleted}))
	d.Update(refreshTickMsg{})
	if d.Phase != session.PhaseDownload {
		t.Fatal("summary appeared before the final download result")
	}
	d.Update(downloadDoneMsg{key: d.Items[0].Item.Key(), err: errors.New("close failed")})
	d.Update(refreshTickMsg{})
	if d.Phase != session.PhaseSummary || len(d.Failures()) != 1 {
		t.Fatal("summary must use the authoritative final download failure")
	}
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if d.Phase != session.PhaseDownload || d.Items[0].Status != model.StatusPending {
		t.Fatal("retry must return to the download table with pending work")
	}
}

func TestSummaryWaitsForRemainingAlbums(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), []model.Album{{Site: "test"}})
	d.Items = []session.Item{{Item: model.MediaItem{Title: "one"}, Status: model.StatusCompleted}}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.Update(refreshTickMsg{})
	if d.Phase != session.PhaseDownload {
		t.Fatal("summary appeared while another album is still being scraped")
	}
}

func TestUntrustedTextCannotEmitTerminalControls(t *testing.T) {
	evil := "x\x1b]52;c;ZXZpbA==\x07\x1b[2J\u009b31m\u202e"
	d := NewBatch(nil, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true, Album: "album" + evil, Site: "site" + evil},
		{Item: model.MediaItem{URL: "https://example.com/1", Title: "title" + evil}, Status: model.StatusDownloading},
	}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	d.AppendError("scrape" + evil)
	d.showErrors = true
	d.Update(downloadDoneMsg{key: d.Items[1].Item.Key(), err: errors.New("error" + evil)})
	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	d.searchQuery = "search" + evil
	views := []string{d.View()}
	d.Phase, d.Items, d.LoadingErr = session.PhaseDone, nil, "load"+evil
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
	d.Items = []session.Item{{Item: model.MediaItem{URL: "https://example.com/1", Title: "file"}, Status: model.StatusPending}}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	d.SetStatus("two  spaces")
	out := d.View()
	for _, text := range []string{"◢◤  SCRAPER  ◢◤", "two  spaces"} {
		if !strings.Contains(out, text) {
			t.Fatalf("view collapsed whitespace in %q:\n%s", text, out)
		}
	}
}

func TestStatsIncludeETA(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Item: model.MediaItem{Title: "a.mp4", Size: 1000}, Status: model.StatusDownloading, Total: 1000, Bytes: 500, Speed: 100},
	}
	if !strings.Contains(d.renderStats(), "eta ") {
		t.Fatalf("stats should include an ETA: %s", d.renderStats())
	}
}

func TestStatsHideETAWhenSizesUnknown(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Item: model.MediaItem{Title: "a.mp4", Size: 1000}, Status: model.StatusDownloading, Total: 1000, Bytes: 500, Speed: 100},
		{Item: model.MediaItem{Title: "b.mp4"}, Status: model.StatusPending},
	}
	if strings.Contains(d.renderStats(), "eta ") {
		t.Fatal("ETA must be hidden when queued items have unknown size")
	}
}

func TestErrorPanelToggles(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Phase = session.PhaseDownload
	d.AppendError("https://bunkr.cr/a/broken: no file links")

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

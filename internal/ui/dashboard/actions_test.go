package dashboard

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/session"
)

func TestCancelSelectedItem(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.cancelled) != 1 {
		t.Fatalf("expected 1 cancelled item, got %d", len(fd.cancelled))
	}
	if fd.cancelled[0].URL != items[0].URL {
		t.Errorf("expected first item cancelled, got %s", fd.cancelled[0].URL)
	}
}

func TestCancelSelectedPendingItem(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4"}}
	d := New(instantItems(items), fd, t.TempDir(), "test")
	d.Items = []session.Item{{Item: items[0], Status: model.StatusPending}}

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.cancelled) != 1 || d.Items[0].Status != model.StatusCancelled {
		t.Fatalf("pending cancel failed: calls=%d status=%v", len(fd.cancelled), d.Items[0].Status)
	}
}

func TestCancelDoesNothingForCompletedItem(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})
	d.applyDone(downloadDoneMsg{key: d.Items[0].Item.Key()})

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.cancelled) != 0 {
		t.Errorf("expected no cancel call for a completed item, got %d", len(fd.cancelled))
	}
}

func TestResultReportsFailuresForExitStatus(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})
	d.applyDone(downloadDoneMsg{key: d.Items[0].Item.Key()})
	if err := d.Result(); err != nil {
		t.Fatalf("Result with no failures = %v, want nil", err)
	}
	d.applyDone(downloadDoneMsg{key: d.Items[1].Item.Key(), err: errors.New("boom")})
	var partial *model.PartialError
	if err := d.Result(); !errors.As(err, &partial) {
		t.Fatalf("Result with a failed item = %v, want *model.PartialError", err)
	}

	dead := New(instantItems(nil), nil, t.TempDir(), "cyberdrop")
	dead.Update(scrapeResultMsg{err: errors.New("404")})
	if err := dead.Result(); err == nil || errors.As(err, &partial) {
		t.Fatalf("a failed single-URL scrape must be a plain error, got %v", err)
	}

	batch := NewBatch(nil, t.TempDir(), []model.Album{{URL: "https://example.com/a/x"}})
	batch.handleBatchScrape(batchScrapeMsg{album: model.Album{URL: "https://example.com/a/x"}, err: errors.New("gone")})
	if err := batch.Result(); !errors.As(err, &partial) {
		t.Fatalf("a failed batch album must be a partial error, got %v", err)
	}
}

func TestCancelSelectedSkipsHeader(t *testing.T) {
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

	// Select the header row (index 0)
	d.table.SetCursor(0)
	preCancel := d.Items[0].Header
	if !preCancel {
		t.Fatal("expected first row to be a header")
	}
	d.cancelSelected()

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.cancelled) != 0 {
		t.Errorf("expected no cancel calls for a header row, got %d", len(fd.cancelled))
	}
}

func TestInspectSelectedSetsInspectMsg(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	d.Items[0].Status = model.StatusFailed
	d.Items[0].Err = errors.New("connection refused")

	d.inspectSelected()

	if !strings.Contains(d.inspectMsg, "connection refused") {
		t.Errorf("expected inspectMsg to contain error, got %q", d.inspectMsg)
	}
}

func TestInspectSelectedOnlyShowsFailedItems(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	d.Items[0].Status = model.StatusCompleted

	d.inspectSelected()

	if d.inspectMsg != "" {
		t.Errorf("expected empty inspectMsg for completed item, got %q", d.inspectMsg)
	}
}

func TestRetrySelectedResetsState(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	wantErr := errors.New("transient error")
	d.applyDone(downloadDoneMsg{key: d.Items[0].Item.Key(), err: wantErr})
	if d.Items[0].Status != model.StatusFailed {
		t.Fatal("expected StatusFailed before retry")
	}

	cmd := d.retrySelected()
	if cmd == nil {
		t.Fatal("expected retry to return a download command")
	}
	cmd() // execute the download

	if d.Items[0].Status != model.StatusPending {
		t.Errorf("expected StatusPending after retry, got %v", d.Items[0].Status)
	}
	if d.Items[0].Err != nil {
		t.Errorf("expected lastErr to be cleared on retry, got %v", d.Items[0].Err)
	}
	if d.Items[0].Bytes != 0 {
		t.Errorf("expected bytes reset to 0, got %d", d.Items[0].Bytes)
	}
}

func TestRetrySelectedReturnsNilForNonFailed(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	d.Items[0].Status = model.StatusCompleted

	if cmd := d.retrySelected(); cmd != nil {
		t.Errorf("expected nil command for completed item")
	}
}

func TestRetrySelectedReturnsNilForHeader(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := New(instantItems(nil), fd, t.TempDir(), "test")
	d.Items = []session.Item{{Header: true}}

	if cmd := d.retrySelected(); cmd != nil {
		t.Errorf("expected nil command for header row")
	}
}

func TestEnterKeyInspectClearsOnNavigation(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	d.Items[0].Status = model.StatusFailed
	d.Items[0].Err = errors.New("boom")
	d.inspectSelected()
	if d.inspectMsg == "" {
		t.Fatal("expected inspectMsg to be set")
	}

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})

	if d.inspectMsg != "" {
		t.Errorf("expected inspectMsg to be cleared on navigation, got %q", d.inspectMsg)
	}
}

func TestStaleFailedProgressDoesNotCorruptRetry(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	// First attempt fails — applyDone stores the error.
	wantErr := errors.New("transient")
	d.applyDone(downloadDoneMsg{key: d.Items[0].Item.Key(), err: wantErr})
	if d.Items[0].Status != model.StatusFailed {
		t.Fatal("expected StatusFailed after applyDone")
	}

	// User retries — state resets to Pending.
	_ = d.retrySelected()
	if d.Items[0].Status != model.StatusPending {
		t.Fatalf("expected StatusPending after retry, got %v", d.Items[0].Status)
	}

	// A stale StatusFailed progress event from the first attempt arrives.
	d.ApplyProgress(model.ProgressEvent{
		Item:   d.Items[0].Item,
		Status: model.StatusFailed,
		Bytes:  0,
		Total:  100,
	})

	// The retried item must not be corrupted back to Failed.
	if d.Items[0].Status == model.StatusFailed {
		t.Fatal("stale StatusFailed progress event corrupted a retried item")
	}
}

func TestFailuresReturnsOnlyFailedItems(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true},
		{Item: model.MediaItem{URL: "https://x/1", Title: "done"}, Status: model.StatusCompleted},
		{Item: model.MediaItem{URL: "https://x/2", Title: "bad"}, Status: model.StatusFailed, Err: errors.New("boom"), AlbumURL: "https://bunkr.cr/a/rBSKhzaL"},
		{Item: model.MediaItem{URL: "https://x/3", Title: "cancelled"}, Status: model.StatusCancelled},
	}

	got := d.Failures()
	if len(got) != 1 {
		t.Fatalf("Failures() returned %d items, want 1", len(got))
	}
	if got[0].Item.Title != "bad" || got[0].Err == nil || got[0].Err.Error() != "boom" {
		t.Fatalf("Failures() = %+v, want the failed item with its error", got[0])
	}
	if got[0].AlbumURL != "https://bunkr.cr/a/rBSKhzaL" {
		t.Fatalf("Failures() AlbumURL = %q, want the album URL", got[0].AlbumURL)
	}
}

func TestFailureJumpWrapsVisibleRows(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true, Album: "mixed"},
		{Item: model.MediaItem{Title: "first"}, Status: model.StatusFailed},
		{Item: model.MediaItem{Title: "pending"}, Status: model.StatusPending},
		{Item: model.MediaItem{Title: "last"}, Status: model.StatusFailed},
	}
	d.refreshRows()
	for _, want := range []int{1, 3, 1} {
		d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
		if got := d.stateAt(d.table.Cursor()); got != want {
			t.Fatalf("F selected state %d, want %d", got, want)
		}
	}
	d.Filter = session.FilterPending
	d.rebuildVisible()
	d.selectRow(1)
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if got := d.stateAt(d.table.Cursor()); got != 2 {
		t.Fatalf("F selected a hidden failure: state %d", got)
	}
}

func TestPendingPreventsSummaryAndCancelAll(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	d := NewBatch(fd, t.TempDir(), nil)
	d.Items = []session.Item{{Item: model.MediaItem{URL: "https://x/1", Title: "one"}, Status: model.StatusPending}, {Item: model.MediaItem{URL: "https://x/2", Title: "two"}, Status: model.StatusDownloading}}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	updated, _ := d.Update(refreshTickMsg{})
	d = updated.(*Dashboard)
	if d.Phase == session.PhaseSummary {
		t.Fatal("pending/downloading items must prevent summary")
	}
	updated, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	d = updated.(*Dashboard)
	if len(fd.cancelled) != 2 || d.Items[0].Status != model.StatusCancelled || d.Items[1].Status != model.StatusCancelled {
		t.Fatalf("cancel-all did not cancel both items")
	}
}

func TestCompletedAlbumAutoFoldsAndManualUnfoldSticks(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true, Album: "a", Site: "s"},
		{Item: model.MediaItem{URL: "https://x/1", Title: "one"}, Status: model.StatusCompleted},
	}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.BatchRemaining = 1 // another album is still scraping, so the summary stays away
	updated, _ := d.Update(refreshTickMsg{})
	d = updated.(*Dashboard)
	if len(d.visible) != 1 || !d.Folded[0] {
		t.Fatalf("completed album did not auto-fold: %v", d.visible)
	}
	updated, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	d = updated.(*Dashboard)
	updated, _ = d.Update(refreshTickMsg{})
	d = updated.(*Dashboard)
	if len(d.visible) != 2 || d.Folded[0] {
		t.Fatalf("manual unfold did not stick: visible=%v folded=%v", d.visible, d.Folded)
	}
}

func TestFailedAlbumStaysOpenWhenOtherAlbumsFold(t *testing.T) {
	d := NewBatch(nil, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true, Album: "complete"},
		{Item: model.MediaItem{Title: "one", Size: 100}, Status: model.StatusCompleted},
		{Header: true, Album: "failed"},
		{Item: model.MediaItem{Title: "two", Size: 100}, Status: model.StatusFailed},
	}
	d.refreshRows()
	d.Phase = session.PhaseDownload
	d.Update(refreshTickMsg{})
	if !d.Folded[0] || d.Folded[2] {
		t.Fatalf("album folds = %v, want only the completed album folded", d.Folded)
	}
	if done, total, _ := d.ProgressSums(); done != 100 || total != 200 {
		t.Fatalf("folding changed global byte totals: %d/%d", done, total)
	}
}

func TestSummaryIgnoresTableKeysAndCanReturnToList(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	d := NewBatch(fd, t.TempDir(), nil)
	d.Items = []session.Item{
		{Item: model.MediaItem{URL: "https://example.com/1", Title: "done"}, Status: model.StatusCompleted},
		{Item: model.MediaItem{URL: "https://example.com/2", Title: "broken"}, Status: model.StatusFailed, Err: errors.New("boom")},
	}
	d.refreshRows()
	d.Phase = session.PhaseSummary
	d.table.SetCursor(1)
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'r'}},
		{Type: tea.KeyRunes, Runes: []rune{'c'}},
		{Type: tea.KeyRunes, Runes: []rune{'C'}},
		{Type: tea.KeyRunes, Runes: []rune{'f'}},
		{Type: tea.KeyRunes, Runes: []rune{'F'}},
		{Type: tea.KeyRunes, Runes: []rune{'/'}},
		{Type: tea.KeyRunes, Runes: []rune{'e'}},
		{Type: tea.KeyTab},
	} {
		if _, cmd := d.Update(key); cmd != nil {
			t.Fatalf("summary key %q started work", key.String())
		}
	}
	if d.Phase != session.PhaseSummary || d.Items[1].Status != model.StatusFailed || d.Filter != session.FilterAll || d.searching || d.showErrors || len(fd.cancelled) != 0 || d.table.Cursor() != 1 {
		t.Fatal("summary keys changed the hidden table")
	}
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if d.Phase != session.PhaseDone || !strings.Contains(d.View(), "broken") {
		t.Fatalf("enter must return to the file list, phase=%v", d.Phase)
	}
	d.Update(refreshTickMsg{})
	if d.Phase != session.PhaseDone {
		t.Fatal("the summary reappeared without new work")
	}
	if _, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd == nil || d.Phase != session.PhaseDownload {
		t.Fatalf("retry from the list must restart the run, phase=%v", d.Phase)
	}
}

func TestRetryAllFailedResetsStates(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Item: model.MediaItem{URL: "https://x/1", Title: "one"}, Status: model.StatusFailed, Err: errors.New("boom")},
		{Item: model.MediaItem{URL: "https://x/2", Title: "two"}, Status: model.StatusCompleted},
		{Item: model.MediaItem{URL: "https://x/3", Title: "three"}, Status: model.StatusFailed, Err: errors.New("boom")},
	}
	d.refreshRows()

	updated, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	got, ok := updated.(*Dashboard)
	if !ok {
		t.Fatal("Update did not return *Dashboard")
	}
	if cmd == nil {
		t.Fatal("expected retry commands for the failed items")
	}
	if got.Items[0].Status != model.StatusPending || got.Items[2].Status != model.StatusPending {
		t.Fatalf("failed items not reset: %v, %v", got.Items[0].Status, got.Items[2].Status)
	}
	if got.Items[1].Status != model.StatusCompleted {
		t.Fatal("completed item must stay completed")
	}
}

func TestSearchJumpsToMatch(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Item: model.MediaItem{Title: "alpha.mp4"}, Status: model.StatusPending},
		{Item: model.MediaItem{Title: "beta.mp4"}, Status: model.StatusPending},
		{Item: model.MediaItem{Title: "gamma.mp4"}, Status: model.StatusPending},
	}
	d.refreshRows()

	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	d2, ok := updated.(*Dashboard)
	if !ok || !d2.searching {
		t.Fatal("pressing / should start search mode")
	}
	updated, _ = d2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("gamma")})
	d3, ok := updated.(*Dashboard)
	if !ok || d3.searchQuery != "gamma" {
		t.Fatalf("search query = %q, want gamma", d3.searchQuery)
	}
	updated, _ = d3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	d4, ok := updated.(*Dashboard)
	if !ok {
		t.Fatal("Update did not return *Dashboard")
	}
	if got := d4.table.Cursor(); got != 2 {
		t.Fatalf("cursor = %d, want 2 (gamma row)", got)
	}
}

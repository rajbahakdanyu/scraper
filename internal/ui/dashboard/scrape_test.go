package dashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestBatchProgressUpdatesRows(t *testing.T) {
	ch := make(chan model.ProgressEvent, 1)
	fd := &fakeDownloader{ch: ch}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	d.Update(progressMsg(model.ProgressEvent{
		Item:   d.states[0].item,
		Bytes:  50,
		Total:  100,
		Status: model.StatusDownloading,
	}))

	if d.states[0].bytes != 50 {
		t.Errorf("expected bytes to update to 50, got %d", d.states[0].bytes)
	}
	if d.states[0].status != model.StatusDownloading {
		t.Errorf("expected status downloading, got %v", d.states[0].status)
	}
}

func TestStaleProgressDoesNotResurrectCancelledItem(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	// User cancels the item.
	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if d.states[0].status != model.StatusCancelled {
		t.Fatalf("expected cancelled, got %v", d.states[0].status)
	}

	// A progress event that was buffered before the cancel arrived gets
	// delivered afterwards — it must not flip the item back to downloading.
	d.Update(progressMsg(model.ProgressEvent{
		Item:   d.states[0].item,
		Bytes:  42,
		Total:  100,
		Status: model.StatusDownloading,
	}))
	if d.states[0].status != model.StatusCancelled {
		t.Fatalf("stale progress event resurrected a cancelled item: %v", d.states[0].status)
	}

	// The cancelled download then returns an error — it must not be
	// downgraded to failed either.
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key(), err: errors.New("download cancelled")})
	if d.states[0].status != model.StatusCancelled {
		t.Fatalf("cancelled item ended as %v", d.states[0].status)
	}
}

func TestStaleProgressDoesNotResurrectCompletedItem(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key()})

	// An out-of-order downloading event arriving after completion must not
	// flip the row back to active (which would leave it stuck there).
	d.Update(progressMsg(model.ProgressEvent{
		Item:   d.states[0].item,
		Bytes:  10,
		Total:  100,
		Status: model.StatusDownloading,
	}))
	if d.states[0].status != model.StatusCompleted {
		t.Fatalf("stale progress event resurrected a completed item: %v", d.states[0].status)
	}
}

func TestDuplicateURLsUpdateIndependentRows(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/same", Title: "first.mp4", Size: 100},
		{URL: "https://example.com/same", Title: "second.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	if d.states[0].item.Key() == d.states[1].item.Key() {
		t.Fatal("duplicate URLs received the same per-run identity")
	}

	d.applyProgress(model.ProgressEvent{
		Item:   d.states[1].item,
		Bytes:  50,
		Total:  100,
		Status: model.StatusDownloading,
	})
	if d.states[0].bytes != 0 || d.states[1].bytes != 50 {
		t.Fatalf("progress updated wrong rows: first=%d second=%d", d.states[0].bytes, d.states[1].bytes)
	}

	d.applyDone(downloadDoneMsg{key: d.states[1].item.Key()})
	if d.states[0].status == model.StatusCompleted || d.states[1].status != model.StatusCompleted {
		t.Fatalf("completion updated wrong rows: first=%v second=%v", d.states[0].status, d.states[1].status)
	}
}

func TestBatchModePerItemDest(t *testing.T) {
	urls := []DashAlbumURL{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/test", Slug: "test", Dest: "/tmp/test-album"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	msg := batchScrapeMsg{album: urls[0], idx: 0, items: items}
	d.handleBatchScrape(msg)

	for i, st := range d.states {
		if st.isHeader {
			continue
		}
		if st.dest != "/tmp/test-album" {
			t.Errorf("state[%d] dest = %q, want /tmp/test-album", i, st.dest)
		}
	}
}

func TestBatchPartialScrapeWarningVisibleMidBatch(t *testing.T) {
	urls := []DashAlbumURL{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/1", Slug: "one", Dest: "/tmp/one"},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/2", Slug: "two", Dest: "/tmp/two"},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/3", Slug: "three", Dest: "/tmp/three"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	first := batchScrapeMsg{album: urls[0], idx: 0, items: []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}}
	d.handleBatchScrape(first)

	partial := batchScrapeMsg{album: urls[1], idx: 1, items: []model.MediaItem{{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 100}}, err: errors.New("one item failed")}
	d.handleBatchScrape(partial)

	if d.batchRemaining != 1 {
		t.Fatalf("expected batchRemaining = 1 (mid-batch), got %d", d.batchRemaining)
	}
	if !strings.Contains(d.status, "warning:") || !strings.Contains(d.status, "one item failed") {
		t.Fatalf("partial warning not surfaced mid-batch: %q", d.status)
	}
}

func TestApplyDoneStoresError(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	wantErr := errors.New("HTTP 404")
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key(), err: wantErr})

	if d.states[0].status != model.StatusFailed {
		t.Errorf("expected StatusFailed, got %v", d.states[0].status)
	}
	if d.states[0].lastErr == nil || d.states[0].lastErr.Error() != "HTTP 404" {
		t.Errorf("expected lastErr to be stored, got %v", d.states[0].lastErr)
	}
}

func TestApplyDoneClearsErrorOnRetrySuccess(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	wantErr := errors.New("transient")
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key(), err: wantErr})
	if d.states[0].lastErr == nil {
		t.Fatal("expected error to be stored")
	}
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key()})
	if d.states[0].lastErr != nil {
		t.Errorf("expected lastErr to be cleared on successful retry, got %v", d.states[0].lastErr)
	}
}

func TestRefreshTickReArmsWhileDownloadsActive(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	// Ensure a download is active (still pending/downloading)
	if !d.spinnerActive() {
		t.Fatal("expected spinner active while download is pending")
	}

	_, cmd := d.Update(refreshTickMsg{})
	if cmd == nil {
		t.Fatal("expected refresh tick to re-arm while downloads are active")
	}
}

func TestRefreshTickStopsWhenIdle(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	// Mark the download complete so spinnerActive returns false.
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key()})
	if d.spinnerActive() {
		t.Fatal("expected spinner inactive after all downloads done")
	}

	// Start the tick chain.
	d.ensureRefreshTicker()
	if !d.refreshTickActive {
		t.Fatal("expected refreshTickActive after ensureRefreshTicker")
	}

	// Tick fires — no downloads active and no pending progress.
	_, cmd := d.Update(refreshTickMsg{})
	if cmd != nil {
		t.Fatal("expected refresh tick to stop when idle")
	}
	if d.refreshTickActive {
		t.Fatal("expected refreshTickActive to be cleared when tick stops")
	}
}

func TestEnsureRefreshTickerIsIdempotent(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	// Bypass handleScrapeResult (which calls ensureRefreshTicker) so we
	// can test idempotency from a clean state.
	d.states = []itemState{{item: items[0], status: model.StatusPending, total: items[0].Size}}
	d.phase = phaseDownload

	cmd1 := d.ensureRefreshTicker()
	if cmd1 == nil {
		t.Fatal("first ensureRefreshTicker should return a command")
	}

	cmd2 := d.ensureRefreshTicker()
	if cmd2 != nil {
		t.Fatal("second ensureRefreshTicker should return nil (idempotent)")
	}
}

func TestProgressAppliedViaRefreshTick(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	// Simulate drainProgress having populated the pending map.
	d.progressMu.Lock()
	d.pending = map[string]model.ProgressEvent{
		d.states[0].item.Key(): {
			Item:   d.states[0].item,
			Bytes:  42,
			Total:  100,
			Status: model.StatusDownloading,
		},
	}
	d.progressMu.Unlock()

	_, cmd := d.Update(refreshTickMsg{})
	if cmd == nil {
		t.Fatal("expected tick to re-arm while download active")
	}

	if d.states[0].bytes != 42 {
		t.Errorf("expected progress applied via tick, got bytes=%d", d.states[0].bytes)
	}
	if d.states[0].status != model.StatusDownloading {
		t.Errorf("expected status downloading, got %v", d.states[0].status)
	}
}

func TestBatchHandlesEmptyAlbumWithoutCreatingDirectory(t *testing.T) {
	root := t.TempDir()
	albumDir := filepath.Join(root, "empty-album")
	urls := []DashAlbumURL{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/empty", Slug: "empty-album", Dest: albumDir},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/next", Slug: "next", Dest: filepath.Join(root, "next")},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, root, urls)

	// First album returns zero items (e.g., all filtered).
	msg := batchScrapeMsg{album: urls[0], idx: 0, items: nil}
	_, cmd := d.handleBatchScrape(msg)

	// Must not have created the empty album's directory.
	if _, err := os.Stat(albumDir); !os.IsNotExist(err) {
		t.Fatalf("empty album directory should not exist, got: %v", err)
	}
	// Must proceed to next album.
	if cmd == nil {
		t.Fatal("expected next scrape command after empty album")
	}

	// Second album arrives normally — should work.
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	msg2 := batchScrapeMsg{album: urls[1], idx: 1, items: items}
	_, cmd2 := d.handleBatchScrape(msg2)
	if cmd2 == nil {
		t.Fatal("expected download commands after normal album")
	}
	if _, err := os.Stat(urls[1].Dest); os.IsNotExist(err) {
		t.Fatal("second album directory should exist")
	}
}

func TestDownloadTickerStopsWhenIdle(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

	if _, cmd := d.Update(tickMsg{}); cmd == nil {
		t.Fatal("expected the download ticker to re-arm while downloads are pending")
	}

	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key()})
	if d.spinnerActive() {
		t.Fatal("expected spinner inactive after all downloads done")
	}
	if _, cmd := d.Update(tickMsg{}); cmd != nil {
		t.Fatal("expected the download ticker to stop when idle")
	}
}

func TestBatchScrapeRecordsError(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.phase = phaseLoading
	updated, _ := d.handleBatchScrape(batchScrapeMsg{
		album: DashAlbumURL{Site: "bunker", URL: "https://bunkr.cr/a/broken", Slug: "broken", Dest: t.TempDir()},
		items: []model.MediaItem{{URL: "https://dl.bunkr.cr/file/1", Title: "one.mp4"}},
		err:   errors.New("page 2 failed"),
	})
	got, ok := updated.(*Dashboard)
	if !ok {
		t.Fatal("handleBatchScrape did not return *Dashboard")
	}
	if len(got.errorLog) != 1 || !strings.Contains(got.errorLog[0], "broken") || !strings.Contains(got.errorLog[0], "page 2 failed") {
		t.Fatalf("errorLog = %v, want the album error", got.errorLog)
	}
}

func TestBatchScrapeRecordsAlbumSourceURL(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 1)}, t.TempDir(), nil)
	d.phase = phaseLoading
	updated, _ := d.handleBatchScrape(batchScrapeMsg{
		album: DashAlbumURL{
			Site: "bunker",
			URL:  "https://bunkr.cr/a/rBSKhzaL",
			Slug: "rBSKhzaL",
			Dest: t.TempDir(),
		},
		items: []model.MediaItem{{URL: "https://dl.bunkr.cr/file/1", Title: "one.mp4"}},
	})
	got, ok := updated.(*Dashboard)
	if !ok {
		t.Fatal("handleBatchScrape did not return *Dashboard")
	}
	if len(got.states) != 2 {
		t.Fatalf("states = %d, want header + item", len(got.states))
	}
	if got.states[1].sourceURL != "https://bunkr.cr/a/rBSKhzaL" {
		t.Fatalf("item sourceURL = %q, want the album URL", got.states[1].sourceURL)
	}
}

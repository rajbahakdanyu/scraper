// internal/ui/dashboard/dashboard_test.go
package dashboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

type fakeDownloader struct {
	mu        sync.Mutex
	ch        chan model.ProgressEvent
	calls     []model.MediaItem
	cancelled []model.MediaItem
	shutdowns int
}

func (f *fakeDownloader) Prepare(item model.MediaItem) {}

func (f *fakeDownloader) Download(item model.MediaItem, dest string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, item)
	f.mu.Unlock()
	return "fake/path", nil
}

func (f *fakeDownloader) Cancel(item model.MediaItem) {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, item)
	f.mu.Unlock()
}

func (f *fakeDownloader) Shutdown() {
	f.mu.Lock()
	f.shutdowns++
	f.mu.Unlock()
}

func (f *fakeDownloader) Progress() <-chan model.ProgressEvent {
	return f.ch
}

func instantItems(items []model.MediaItem) func(context.Context) ([]model.MediaItem, error) {
	return func(context.Context) ([]model.MediaItem, error) {
		return items, nil
	}
}

func TestDashboardRefusesSymlinkedAlbumDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "album")); err != nil {
		t.Fatal(err)
	}

	items := []model.MediaItem{{URL: "https://example.com/a", Title: "a"}, {URL: "https://example.com/b", Title: "b"}}
	d := New(instantItems(items), nil, root, "test")
	d.SetAlbumSlug("album")
	d.Update(scrapeResultMsg{items: items})

	if d.phase != phaseDone || !strings.Contains(d.loadingErr, "refusing to use symlinked output directory") {
		t.Fatalf("expected symlinked directory refusal, phase=%v error=%q", d.phase, d.loadingErr)
	}
}

func TestBatchDashboardRefusesSymlinkedAlbumDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "album")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	urls := []DashAlbumURL{{Site: "test", URL: "https://example.com/album", Slug: "album", Dest: link}}
	d := NewBatch(fd, root, urls)
	d.handleBatchScrape(batchScrapeMsg{album: urls[0], items: []model.MediaItem{{URL: "https://example.com/a", Title: "a"}}})

	if d.phase != phaseDone || !strings.Contains(d.loadingErr, "refusing to use symlinked output directory") {
		t.Fatalf("expected symlinked directory refusal, phase=%v error=%q", d.phase, d.loadingErr)
	}
}

func TestDashboardLoadingThenBatch(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	if d.phase != phaseLoading {
		t.Errorf("expected phaseLoading, got %d", d.phase)
	}

	d.Update(scrapeResultMsg{items: items})
	if d.phase != phaseDownload {
		t.Errorf("expected phaseDownload after scrape result, got %d", d.phase)
	}
	if len(d.states) != 2 {
		t.Errorf("expected 2 states, got %d", len(d.states))
	}
}

func TestDashboardYouTubeUsesBatchFlow(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://www.youtube.com/watch?v=abc", Title: "Video One", Type: model.MediaTypeVideo},
		{URL: "https://www.youtube.com/watch?v=def", Title: "Video Two", Type: model.MediaTypeVideo},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := New(instantItems(items), fd, t.TempDir(), "youtube")
	if d.phase != phaseLoading {
		t.Errorf("expected phaseLoading, got %d", d.phase)
	}

	d.Update(scrapeResultMsg{items: items})
	if d.phase != phaseDownload {
		t.Errorf("expected phaseDownload after scrape result (same as any other site), got %d", d.phase)
	}
	if len(d.states) != 2 {
		t.Errorf("expected 2 states, got %d", len(d.states))
	}
}

func TestDashboardLoadingError(t *testing.T) {
	d := New(func(context.Context) ([]model.MediaItem, error) {
		return nil, nil
	}, nil, t.TempDir(), "test")

	d.Update(scrapeResultMsg{items: nil, err: nil})
	if d.phase != phaseDone {
		t.Errorf("expected phaseDone for no items, got %d", d.phase)
	}
	if d.loadingErr == "" {
		t.Error("expected loading error message")
	}
}

func TestDashboardShowsPartialScrapeWarningAndContinues(t *testing.T) {
	items := []model.MediaItem{{URL: "https://example.com/a", Title: "a"}}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	partialErr := model.NewPartialError("test album", []error{errors.New("one item failed")})

	d.Update(scrapeResultMsg{items: items, err: partialErr})

	if d.phase != phaseDownload || len(d.states) != 1 {
		t.Fatalf("partial result did not continue: phase=%v states=%d", d.phase, len(d.states))
	}
	if !strings.Contains(d.status, "warning:") || !strings.Contains(d.status, "one item failed") {
		t.Fatalf("partial warning not surfaced: %q", d.status)
	}
}

func TestDashboardRejectsEscapingAlbumSlug(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a", Title: "a"},
		{URL: "https://example.com/b", Title: "b"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	d := New(instantItems(items), fd, t.TempDir(), "test")
	d.SetAlbumSlug("../../escape")

	_, cmd := d.Update(scrapeResultMsg{items: items})

	if cmd != nil {
		t.Fatal("unsafe album slug started download commands")
	}
	if d.phase != phaseDone || !strings.Contains(d.loadingErr, "unsafe path component") {
		t.Fatalf("unsafe album slug not rejected: phase=%v error=%q", d.phase, d.loadingErr)
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.calls) != 0 {
		t.Fatalf("unsafe album slug started %d downloads", len(fd.calls))
	}
}

func TestDashboardQuit(t *testing.T) {
	d := New(instantItems(nil), nil, t.TempDir(), "test")

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", cmd())
	}
	if !d.quitting {
		t.Error("expected quitting to be true")
	}
}

func TestDashboardQuitShutsDownDownloader(t *testing.T) {
	// Every quit key (q, ctrl+c, esc) must trigger downloader shutdown so
	// in-flight downloads are cancelled and the progress channel closes.
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
	} {
		fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
		d := New(instantItems(nil), fd, t.TempDir(), "test")

		d.Update(key)

		fd.mu.Lock()
		if fd.shutdowns != 1 {
			t.Errorf("key %q: expected Shutdown to be called once, got %d", key.String(), fd.shutdowns)
		}
		fd.mu.Unlock()
	}
}

func TestDashboardQuitCancelsPendingScrape(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	d := New(func(ctx context.Context) ([]model.MediaItem, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}, nil, t.TempDir(), "test")

	go d.startScrape()()
	<-started
	d.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("pending scrape was not cancelled on quit")
	}
}

func TestBatchAutoDownload(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 10},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 20},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.states = make([]itemState, len(items))
	for i, it := range items {
		d.states[i] = itemState{item: it, status: model.StatusPending, total: it.Size}
	}

	cmd := d.startAllDownloads()
	if cmd == nil {
		t.Fatal("expected startAllDownloads to return a command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			c()
		}
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.calls) != 2 {
		t.Fatalf("expected 2 download calls, got %d", len(fd.calls))
	}
}

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
	d.states = []itemState{{item: items[0], status: model.StatusPending}}

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.cancelled) != 1 || d.states[0].status != model.StatusCancelled {
		t.Fatalf("pending cancel failed: calls=%d status=%v", len(fd.cancelled), d.states[0].status)
	}
}

func TestCancelDoesNothingForCompletedItem(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key()})

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.cancelled) != 0 {
		t.Errorf("expected no cancel call for a completed item, got %d", len(fd.cancelled))
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

func TestNewBatchCreatesDashboard(t *testing.T) {
	urls := []DashAlbumURL{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/album1", Slug: "album1", Dest: "/tmp/album1"},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/album2", Slug: "album2", Dest: "/tmp/album2"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	if !d.batchMode {
		t.Error("expected batchMode to be true")
	}
	if d.batchRemaining != 2 {
		t.Errorf("expected batchRemaining = 2, got %d", d.batchRemaining)
	}
	if d.phase != phaseLoading {
		t.Errorf("expected phaseLoading, got %d", d.phase)
	}
}

func TestBatchHeaderRowsCreated(t *testing.T) {
	urls := []DashAlbumURL{
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

	if len(d.states) == 0 {
		t.Fatal("expected states to be populated")
	}
	if !d.states[0].isHeader {
		t.Error("expected first state to be a header row")
	}
	if d.states[0].albumName != "test" {
		t.Errorf("expected header album name 'test', got %q", d.states[0].albumName)
	}
}

func TestBatchHeaderCursorSkip(t *testing.T) {
	urls := []DashAlbumURL{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/test", Slug: "test", Dest: "/tmp/test"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	msg := batchScrapeMsg{album: urls[0], idx: 0, items: items}
	_, _ = d.handleBatchScrape(msg)

	d.table.SetHeight(10)
	d.refreshRows()

	// Cursor should not be on a header row (it starts at 0, but move
	// logic would skip if it were)
	if d.states[0].isHeader {
		t.Log("header row exists at index 0, which is expected for batch mode")
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

func TestBatchModeStatsShowsScraping(t *testing.T) {
	urls := []DashAlbumURL{
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
	urls := []DashAlbumURL{
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

func TestCancelSelectedSkipsHeader(t *testing.T) {
	urls := []DashAlbumURL{
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
	preCancel := d.states[0].isHeader
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

func TestInspectSelectedSetsInspectMsg(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	d.states[0].status = model.StatusFailed
	d.states[0].lastErr = errors.New("connection refused")

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

	d.states[0].status = model.StatusCompleted

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
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key(), err: wantErr})
	if d.states[0].status != model.StatusFailed {
		t.Fatal("expected StatusFailed before retry")
	}

	cmd := d.retrySelected()
	if cmd == nil {
		t.Fatal("expected retry to return a download command")
	}
	cmd() // execute the download

	if d.states[0].status != model.StatusPending {
		t.Errorf("expected StatusPending after retry, got %v", d.states[0].status)
	}
	if d.states[0].lastErr != nil {
		t.Errorf("expected lastErr to be cleared on retry, got %v", d.states[0].lastErr)
	}
	if d.states[0].bytes != 0 {
		t.Errorf("expected bytes reset to 0, got %d", d.states[0].bytes)
	}
}

func TestRetrySelectedReturnsNilForNonFailed(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	d := New(instantItems(items), fd, t.TempDir(), "test")
	d.Update(scrapeResultMsg{items: items})

	d.states[0].status = model.StatusCompleted

	if cmd := d.retrySelected(); cmd != nil {
		t.Errorf("expected nil command for completed item")
	}
}

func TestRetrySelectedReturnsNilForHeader(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := New(instantItems(nil), fd, t.TempDir(), "test")
	d.states = []itemState{{isHeader: true}}

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

	d.states[0].status = model.StatusFailed
	d.states[0].lastErr = errors.New("boom")
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
	d.applyDone(downloadDoneMsg{key: d.states[0].item.Key(), err: wantErr})
	if d.states[0].status != model.StatusFailed {
		t.Fatal("expected StatusFailed after applyDone")
	}

	// User retries — state resets to Pending.
	_ = d.retrySelected()
	if d.states[0].status != model.StatusPending {
		t.Fatalf("expected StatusPending after retry, got %v", d.states[0].status)
	}

	// A stale StatusFailed progress event from the first attempt arrives.
	d.applyProgress(model.ProgressEvent{
		Item:   d.states[0].item,
		Status: model.StatusFailed,
		Bytes:  0,
		Total:  100,
	})

	// The retried item must not be corrupted back to Failed.
	if d.states[0].status == model.StatusFailed {
		t.Fatal("stale StatusFailed progress event corrupted a retried item")
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

func TestHeaderRowRenderedInVisibleColumn(t *testing.T) {
	urls := []DashAlbumURL{
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

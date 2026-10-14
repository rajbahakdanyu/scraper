// internal/ui/dashboard/dashboard_test.go
package dashboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

func TestDashboardNewNavigationAndProgressFeatures(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 4)}, t.TempDir(), nil)
	d.states = []itemState{
		{isHeader: true, albumName: "a", albumSite: "site"},
		{item: model.MediaItem{URL: "https://x/1", Title: "one.mp4", Size: 100}, status: model.StatusFailed, lastErr: errors.New("gone")},
		{item: model.MediaItem{URL: "https://x/2", Title: "two.mp4"}, status: model.StatusPending},
		{item: model.MediaItem{URL: "https://x/3", Title: "three.mp4"}, status: model.StatusPending},
	}
	d.refreshRows()
	if len(d.visible) != 4 {
		t.Fatalf("visible identity = %v", d.visible)
	}
	d.table.SetCursor(0)
	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	d = updated.(*Dashboard)
	if d.table.Cursor() != 1 {
		t.Fatalf("F cursor = %d, want failed row 1", d.table.Cursor())
	}
	if got := d.rows[2][3]; !strings.Contains(got, "queued #1") {
		t.Fatalf("pending queue cell = %q", got)
	}
	if got := renderBar(5, 10, 4); got != "▓▓░░" {
		t.Fatalf("renderBar = %q", got)
	}

	updated, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	d = updated.(*Dashboard)
	if d.filter != filterFailed || len(d.visible) != 2 || d.visible[0] != 0 || d.visible[1] != 1 {
		t.Fatalf("failed filter visible = %v", d.visible)
	}
	if !strings.Contains(d.renderStats(), "/3") {
		t.Fatalf("filtered stats should remain global: %q", d.renderStats())
	}

	updated, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	d = updated.(*Dashboard)
	if len(d.visible) != 1 || d.visible[0] != 0 {
		t.Fatalf("fold with filter should retain only its album header, got %v", d.visible)
	}
	updated, _ = d.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	d = updated.(*Dashboard)
	if d.table.Cursor() < 0 || d.table.Cursor() >= len(d.visible) {
		t.Fatalf("wheel cursor outside visible rows: %d", d.table.Cursor())
	}
}

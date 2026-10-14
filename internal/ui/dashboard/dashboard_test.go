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
	"github.com/rajbahakdanyu/scraper/internal/session"
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

	if d.Phase != session.PhaseDone || !strings.Contains(d.LoadingErr, "refusing to use symlinked output directory") {
		t.Fatalf("expected symlinked directory refusal, phase=%v error=%q", d.Phase, d.LoadingErr)
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
	urls := []model.Album{{Site: "test", URL: "https://example.com/album", Slug: "album", Dest: link}}
	d := NewBatch(fd, root, urls)
	d.handleBatchScrape(batchScrapeMsg{album: urls[0], items: []model.MediaItem{{URL: "https://example.com/a", Title: "a"}}})

	if d.Phase != session.PhaseDone || !strings.Contains(d.LoadingErr, "refusing to use symlinked output directory") {
		t.Fatalf("expected symlinked directory refusal, phase=%v error=%q", d.Phase, d.LoadingErr)
	}
}

func TestDashboardLoadingThenBatch(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	if d.Phase != session.PhaseLoading {
		t.Errorf("expected session.PhaseLoading, got %d", d.Phase)
	}

	d.Update(scrapeResultMsg{items: items})
	if d.Phase != session.PhaseDownload {
		t.Errorf("expected session.PhaseDownload after scrape result, got %d", d.Phase)
	}
	if len(d.Items) != 2 {
		t.Errorf("expected 2 states, got %d", len(d.Items))
	}
}

func TestDashboardYouTubeUsesBatchFlow(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://www.youtube.com/watch?v=abc", Title: "Video One", Type: model.MediaTypeVideo},
		{URL: "https://www.youtube.com/watch?v=def", Title: "Video Two", Type: model.MediaTypeVideo},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := New(instantItems(items), fd, t.TempDir(), "youtube")
	if d.Phase != session.PhaseLoading {
		t.Errorf("expected session.PhaseLoading, got %d", d.Phase)
	}

	d.Update(scrapeResultMsg{items: items})
	if d.Phase != session.PhaseDownload {
		t.Errorf("expected session.PhaseDownload after scrape result (same as any other site), got %d", d.Phase)
	}
	if len(d.Items) != 2 {
		t.Errorf("expected 2 states, got %d", len(d.Items))
	}
}

func TestDashboardLoadingError(t *testing.T) {
	d := New(func(context.Context) ([]model.MediaItem, error) {
		return nil, nil
	}, nil, t.TempDir(), "test")

	d.Update(scrapeResultMsg{items: nil, err: nil})
	if d.Phase != session.PhaseDone {
		t.Errorf("expected session.PhaseDone for no items, got %d", d.Phase)
	}
	if d.LoadingErr == "" {
		t.Error("expected loading error message")
	}
}

func TestDashboardShowsPartialScrapeWarningAndContinues(t *testing.T) {
	items := []model.MediaItem{{URL: "https://example.com/a", Title: "a"}}
	d := New(instantItems(items), nil, t.TempDir(), "test")
	partialErr := model.NewPartialError("test album", []error{errors.New("one item failed")})

	d.Update(scrapeResultMsg{items: items, err: partialErr})

	if d.Phase != session.PhaseDownload || len(d.Items) != 1 {
		t.Fatalf("partial result did not continue: phase=%v states=%d", d.Phase, len(d.Items))
	}
	if !strings.Contains(d.Status, "warning:") || !strings.Contains(d.Status, "one item failed") {
		t.Fatalf("partial warning not surfaced: %q", d.Status)
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
	if d.Phase != session.PhaseDone || !strings.Contains(d.LoadingErr, "unsafe path component") {
		t.Fatalf("unsafe album slug not rejected: phase=%v error=%q", d.Phase, d.LoadingErr)
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

	go d.taskCmd(session.Task{Kind: session.TaskScrape})()
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
	d.Items = make([]session.Item, len(items))
	for i, it := range items {
		d.Items[i] = session.Item{Item: it, Status: model.StatusPending, Total: it.Size}
	}

	cmd := d.runTasks(d.StartAllDownloads())
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
	urls := []model.Album{
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/album1", Slug: "album1", Dest: "/tmp/album1"},
		{Site: "cyberdrop", URL: "https://cyberdrop.cr/a/album2", Slug: "album2", Dest: "/tmp/album2"},
	}
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	d := NewBatch(fd, "/tmp", urls)

	if !d.BatchMode {
		t.Error("expected batchMode to be true")
	}
	if d.BatchRemaining != 2 {
		t.Errorf("expected batchRemaining = 2, got %d", d.BatchRemaining)
	}
	if d.Phase != session.PhaseLoading {
		t.Errorf("expected session.PhaseLoading, got %d", d.Phase)
	}
}

func TestDashboardNewNavigationAndProgressFeatures(t *testing.T) {
	d := NewBatch(&fakeDownloader{ch: make(chan model.ProgressEvent, 4)}, t.TempDir(), nil)
	d.Items = []session.Item{
		{Header: true, Album: "a", Site: "site"},
		{Item: model.MediaItem{URL: "https://x/1", Title: "one.mp4", Size: 100}, Status: model.StatusFailed, Err: errors.New("gone")},
		{Item: model.MediaItem{URL: "https://x/2", Title: "two.mp4"}, Status: model.StatusPending},
		{Item: model.MediaItem{URL: "https://x/3", Title: "three.mp4"}, Status: model.StatusPending},
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
	if d.Filter != session.FilterFailed || len(d.visible) != 2 || d.visible[0] != 0 || d.visible[1] != 1 {
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

// internal/ui/dashboard/dashboard_test.go
package dashboard

import (
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/user/scraper/internal/model"
)

type fakeDownloader struct {
	mu    sync.Mutex
	ch    chan model.ProgressEvent
	calls []model.MediaItem
}

func (f *fakeDownloader) Download(item model.MediaItem, dest string) error {
	f.mu.Lock()
	f.calls = append(f.calls, item)
	f.mu.Unlock()
	return nil
}

func (f *fakeDownloader) Progress() <-chan model.ProgressEvent {
	return f.ch
}

func TestDashboardInit(t *testing.T) {
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	d := New(items, nil, t.TempDir(), "test")
	if d == nil {
		t.Fatal("expected dashboard to initialize")
	}

	if cmd := d.Init(); cmd == nil {
		t.Error("expected Init to return a command")
	}
}

func TestDashboardToggleSelection(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}
	d := New(items, nil, t.TempDir(), "test")

	newModel, _ := d.Update(tea.KeyMsg{Type: tea.KeySpace})
	dash, ok := newModel.(*Dashboard)
	if !ok {
		t.Fatalf("expected *Dashboard, got %T", newModel)
	}
	if !dash.states[0].selected {
		t.Error("expected first item to become selected after space")
	}

	newModel, _ = dash.Update(tea.KeyMsg{Type: tea.KeySpace})
	dash = newModel.(*Dashboard)
	if dash.states[0].selected {
		t.Error("expected first item to become deselected after second space")
	}
}

func TestDashboardQuit(t *testing.T) {
	items := []model.MediaItem{{URL: "u", Title: "t"}}
	d := New(items, nil, t.TempDir(), "test")

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

func TestStartDownloadCommand(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	item := model.MediaItem{URL: "https://example.com/a.mp4", Title: "a.mp4"}

	cmd := startDownload(fd, item, t.TempDir())
	msg := cmd()

	done, ok := msg.(downloadDoneMsg)
	if !ok {
		t.Fatalf("expected downloadDoneMsg, got %T", msg)
	}
	if done.url != item.URL {
		t.Errorf("unexpected url in msg: %s", done.url)
	}
	if done.err != nil {
		t.Errorf("unexpected error: %v", done.err)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.calls) != 1 {
		t.Fatalf("expected 1 download call, got %d", len(fd.calls))
	}
}

func TestDashboardEnterTriggersDownload(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 10}}
	d := New(items, fd, t.TempDir(), "test")

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a non-nil command after pressing enter")
	}
	cmd()

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.calls) != 1 {
		t.Fatalf("expected 1 download call, got %d", len(fd.calls))
	}
}

func TestDashboardEnterDoesNotRedownload(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 1)}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 10}}
	d := New(items, fd, t.TempDir(), "test")

	d.states[0].status = model.StatusCompleted
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		cmd()
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.calls) != 0 {
		t.Fatalf("expected 0 download calls for completed item, got %d", len(fd.calls))
	}
}

func TestDashboardProgressUpdatesRows(t *testing.T) {
	ch := make(chan model.ProgressEvent, 1)
	fd := &fakeDownloader{ch: ch}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	d := New(items, fd, t.TempDir(), "test")

	newModel, cmd := d.Update(progressMsg(model.ProgressEvent{
		Item:   items[0],
		Bytes:  50,
		Total:  100,
		Status: model.StatusDownloading,
	}))
	dash := newModel.(*Dashboard)
	if dash.states[0].bytes != 50 {
		t.Errorf("expected bytes to update to 50, got %d", dash.states[0].bytes)
	}
	if dash.states[0].status != model.StatusDownloading {
		t.Errorf("expected status downloading, got %v", dash.states[0].status)
	}
	if cmd == nil {
		t.Error("expected dashboard to keep listening for more progress events")
	}
}

func TestDashboardViewContainsStats(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}
	d := New(items, nil, t.TempDir(), "youtube")

	d.width = 100
	d.height = 30
	d.ready = true
	d.table.SetWidth(96)
	d.progress.Width = 76

	view := d.View()
	if !strings.Contains(view, "Pending") {
		t.Error("expected view to contain stats")
	}
	if !strings.Contains(view, "youtube") {
		t.Error("expected view to contain site name")
	}
}

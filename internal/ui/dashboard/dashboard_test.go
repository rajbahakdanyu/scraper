// internal/ui/dashboard/dashboard_test.go
package dashboard

import (
	"sync"
	"testing"
	"time"

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

func TestBatchAutoDownload(t *testing.T) {
	fd := &fakeDownloader{ch: make(chan model.ProgressEvent, 10)}
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 10},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 20},
	}
	d := New(items, fd, t.TempDir(), "cyberdrop")

	cmd := d.startAllDownloads()
	if cmd == nil {
		t.Fatal("expected startAllDownloads to return a command")
	}
	// tea.Batch returns a BatchMsg; execute each sub-command manually
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			c()
		}
	} else {
		cmd()
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.calls) != 2 {
		t.Fatalf("expected 2 download calls, got %d", len(fd.calls))
	}
}

func TestYouTubeStartsInSelectPhase(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/360", Title: "Video [360p] (muxed)", Type: model.MediaTypeVideo},
		{URL: "https://example.com/1080", Title: "Video [1080p] (video-only)", Type: model.MediaTypeVideo},
	}
	d := New(items, nil, t.TempDir(), "youtube")

	if d.phase != phaseSelect {
		t.Errorf("expected phaseSelect, got %d", d.phase)
	}
}

type fakeYTDownloader struct {
	called bool
	height int
}

func (f *fakeYTDownloader) DownloadVideoWithQuality(videoID, dest string, height int) error {
	f.called = true
	f.height = height
	return nil
}

func TestYouTubeEnterTriggersDownload(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/360", Title: "Video [360p] (muxed)", Type: model.MediaTypeVideo},
		{URL: "https://example.com/1080", Title: "Video [1080p] (video-only)", Type: model.MediaTypeVideo},
	}
	d := New(items, nil, t.TempDir(), "youtube")

	fyd := &fakeYTDownloader{}
	d.SetYouTubeDownloader(fyd, "videoid123")

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil command from Enter")
	}

	msg := cmd()
	doneMsg, ok := msg.(youtubeDoneMsg)
	if !ok {
		t.Fatalf("expected youtubeDoneMsg, got %T", msg)
	}
	// Feed the done message back into Update
	d.Update(doneMsg)

	if !fyd.called {
		t.Error("expected DownloadVideoWithQuality to be called")
	}
	if fyd.height != 360 {
		t.Errorf("expected height 360, got %d", fyd.height)
	}

	if d.phase != phaseDone {
		t.Errorf("expected phaseDone, got %d", d.phase)
	}
}

func TestYouTubeEnterBlockedDuringDownload(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/360", Title: "Video [360p] (muxed)", Type: model.MediaTypeVideo},
	}
	d := New(items, nil, t.TempDir(), "youtube")

	fyd := &fakeYTDownloader{}
	d.SetYouTubeDownloader(fyd, "videoid123")

	// Start download
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command from first Enter")
	}

	// While downloading, Enter should do nothing
	d.phase = phaseDownload
	_, cmd2 := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 != nil {
		t.Error("expected nil command during download")
	}
}

func TestYouTubeProgressUpdates(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/1080", Title: "Video [1080p] (video-only)", Size: 1000, Type: model.MediaTypeVideo},
	}
	d := New(items, nil, t.TempDir(), "youtube")
	d.SetYouTubeDownloader(&fakeYTDownloader{}, "vid")
	d.phase = phaseDownload
	d.ytStarted = time.Now()

	d.Update(progressMsg(model.ProgressEvent{
		Bytes:  500,
		Total:  1000,
		Status: model.StatusDownloading,
	}))

	if d.ytBytes != 500 {
		t.Errorf("expected ytBytes 500, got %d", d.ytBytes)
	}
	if d.ytTotal != 1000 {
		t.Errorf("expected ytTotal 1000, got %d", d.ytTotal)
	}
}

func TestBatchProgressUpdatesRows(t *testing.T) {
	ch := make(chan model.ProgressEvent, 1)
	fd := &fakeDownloader{ch: ch}
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	d := New(items, fd, t.TempDir(), "cyberdrop")

	d.Update(progressMsg(model.ProgressEvent{
		Item:   items[0],
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

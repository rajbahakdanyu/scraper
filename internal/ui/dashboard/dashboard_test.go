// internal/ui/dashboard/dashboard_test.go
package dashboard

import (
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

func instantItems(items []model.MediaItem) func() ([]model.MediaItem, error) {
	return func() ([]model.MediaItem, error) {
		return items, nil
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

func TestDashboardLoadingThenYouTubeSelect(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/360", Title: "Video [360p] (muxed)", Type: model.MediaTypeVideo},
		{URL: "https://example.com/1080", Title: "Video [1080p] (video-only)", Type: model.MediaTypeVideo},
	}
	d := New(instantItems(items), nil, t.TempDir(), "youtube")
	if d.phase != phaseLoading {
		t.Errorf("expected phaseLoading, got %d", d.phase)
	}

	d.Update(scrapeResultMsg{items: items})
	if d.phase != phaseSelect {
		t.Errorf("expected phaseSelect after scrape result, got %d", d.phase)
	}
}

func TestDashboardLoadingError(t *testing.T) {
	d := New(func() ([]model.MediaItem, error) {
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
	d := New(instantItems(items), nil, t.TempDir(), "youtube")
	d.Update(scrapeResultMsg{items: items})

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
	d := New(instantItems(items), nil, t.TempDir(), "youtube")
	d.Update(scrapeResultMsg{items: items})

	fyd := &fakeYTDownloader{}
	d.SetYouTubeDownloader(fyd, "videoid123")

	d.phase = phaseDownload
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected nil command during download")
	}
}

func TestYouTubeProgressUpdates(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://example.com/1080", Title: "Video [1080p] (video-only)", Size: 1000, Type: model.MediaTypeVideo},
	}
	d := New(instantItems(items), nil, t.TempDir(), "youtube")
	d.Update(scrapeResultMsg{items: items})
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
	d := New(instantItems(items), fd, t.TempDir(), "cyberdrop")
	d.Update(scrapeResultMsg{items: items})

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

// internal/ui/dashboard/dashboard_test.go
package dashboard

import (
	"errors"
	"strings"
	"sync"
	"testing"
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
}

func (f *fakeDownloader) Download(item model.MediaItem, dest string) error {
	f.mu.Lock()
	f.calls = append(f.calls, item)
	f.mu.Unlock()
	return nil
}

func (f *fakeDownloader) Cancel(item model.MediaItem) {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, item)
	f.mu.Unlock()
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

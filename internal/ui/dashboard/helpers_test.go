package dashboard

import (
	"context"
	"strings"
	"sync"

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

// lines counts rendered lines in a lipgloss string.
func lines(s string) int {
	return strings.Count(s, "\n") + 1
}

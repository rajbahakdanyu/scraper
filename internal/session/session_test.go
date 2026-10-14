package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

type fakeDownloader struct {
	mu        sync.Mutex
	ch        chan model.ProgressEvent
	prepared  []model.MediaItem
	cancelled []model.MediaItem
	errs      map[string]error
	shutdowns int
}

func newFakeDownloader() *fakeDownloader {
	return &fakeDownloader{ch: make(chan model.ProgressEvent, 16), errs: map[string]error{}}
}

func (f *fakeDownloader) Prepare(item model.MediaItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = append(f.prepared, item)
}

func (f *fakeDownloader) Download(item model.MediaItem, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "path", f.errs[item.URL]
}

func (f *fakeDownloader) Cancel(item model.MediaItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, item)
}

func (f *fakeDownloader) Shutdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.shutdowns == 0 {
		close(f.ch)
	}
	f.shutdowns++
}

func (f *fakeDownloader) Progress() <-chan model.ProgressEvent { return f.ch }

type fakeScraper struct {
	items []model.MediaItem
	err   error
}

func (f fakeScraper) Name() string { return "fake" }

func (f fakeScraper) Parse(context.Context, string) ([]model.MediaItem, error) {
	return append([]model.MediaItem(nil), f.items...), f.err
}

// run executes tasks to completion synchronously, as a frontend would.
func run(s *Session, tasks []Task) {
	for len(tasks) > 0 {
		t := tasks[0]
		tasks = append(tasks[1:], s.Apply(s.Exec(t))...)
	}
}

func items(urls ...string) []model.MediaItem {
	var out []model.MediaItem
	for _, u := range urls {
		out = append(out, model.MediaItem{URL: "https://x/" + u, Title: u, Size: 100})
	}
	return out
}

func TestSingleRunDownloadsIntoAlbumFolderAndSummarizes(t *testing.T) {
	dl := newFakeDownloader()
	dl.errs["https://x/b.mp4"] = errors.New("boom")
	s := New(func(context.Context) ([]model.MediaItem, error) { return items("a.mp4", "b.mp4"), nil }, dl, t.TempDir(), "test")
	s.AlbumSlug = "album"
	s.SourceURL = "https://x/a/album"

	run(s, s.Start())

	if s.Phase != PhaseDownload {
		t.Fatalf("phase = %v, want download", s.Phase)
	}
	if s.Items[0].Status != model.StatusCompleted || s.Items[1].Status != model.StatusFailed {
		t.Fatalf("statuses = %v, %v", s.Items[0].Status, s.Items[1].Status)
	}
	if s.Refresh() || s.Phase != PhaseSummary {
		t.Fatalf("a settled run must move to the summary and stop refreshing, phase = %v", s.Phase)
	}
	f := s.Failures()
	if len(f) != 1 || f[0].AlbumURL != "https://x/a/album" {
		t.Fatalf("Failures() = %+v", f)
	}
	var partial *model.PartialError
	if !errors.As(s.Result(), &partial) {
		t.Fatalf("Result() = %v, want a partial error", s.Result())
	}

	delete(dl.errs, "https://x/b.mp4")
	tasks := s.Retry(1)
	if len(tasks) != 1 || s.Phase != PhaseDownload || s.Items[1].Status != model.StatusPending {
		t.Fatalf("retry: tasks=%d phase=%v status=%v", len(tasks), s.Phase, s.Items[1].Status)
	}
	run(s, tasks)
	if s.Result() != nil {
		t.Fatalf("Result() after a successful retry = %v", s.Result())
	}
}

func TestSingleRunScrapeFailureIsPlainError(t *testing.T) {
	s := New(func(context.Context) ([]model.MediaItem, error) { return nil, errors.New("404") }, nil, t.TempDir(), "test")
	run(s, s.Start())
	var partial *model.PartialError
	if s.Phase != PhaseDone || s.Result() == nil || errors.As(s.Result(), &partial) {
		t.Fatalf("phase=%v result=%v", s.Phase, s.Result())
	}
}

func TestBatchRunAppendsAlbumsInOrder(t *testing.T) {
	dl := newFakeDownloader()
	root := t.TempDir()
	albums := []model.Album{
		{Site: "a", URL: "https://x/a/1", Slug: "one", Dest: root + "/one", Scraper: fakeScraper{items: items("a.mp4")}},
		{Site: "a", URL: "https://x/a/2", Slug: "two", Dest: root + "/two", Scraper: fakeScraper{err: errors.New("gone")}},
		{Site: "a", URL: "https://x/a/3", Slug: "three", Dest: root + "/three", Scraper: fakeScraper{items: items("a.mp4", "c.mp4")}},
	}
	s := NewBatch(dl, root, albums)
	run(s, s.Start())

	if s.BatchRemaining != 0 || len(s.ScrapeErrs) != 1 || len(s.ErrorLog) != 1 {
		t.Fatalf("remaining=%d scrapeErrs=%v log=%v", s.BatchRemaining, s.ScrapeErrs, s.ErrorLog)
	}
	if len(s.Items) != 5 || !s.Items[0].Header || !s.Items[2].Header || s.Items[2].Album != "three" {
		t.Fatalf("items = %+v", s.Items)
	}
	if s.Items[1].Item.Key() == s.Items[3].Item.Key() {
		t.Fatal("duplicate URLs across albums must get distinct keys")
	}
	if s.Items[3].Dest != root+"/three" || s.Items[3].AlbumURL != "https://x/a/3" {
		t.Fatalf("item dest/album = %q/%q", s.Items[3].Dest, s.Items[3].AlbumURL)
	}
	if !s.AutoFold() || !s.Folded[0] || !s.Folded[2] {
		t.Fatalf("finished albums must auto-fold: %v", s.Folded)
	}
	if v := s.Visible(); len(v) != 2 {
		t.Fatalf("folded albums show only headers, got %v", v)
	}
	s.ToggleFold(1)
	if s.Folded[0] || !s.ManuallyUnfolded[0] {
		t.Fatal("toggling an item's album must unfold it by hand")
	}
	if s.AutoFold(); s.Folded[0] {
		t.Fatal("a manually unfolded album must not auto-fold again")
	}
}

func TestProgressNeverResurrectsTerminalItems(t *testing.T) {
	s := New(nil, nil, t.TempDir(), "test")
	s.Items = []Item{{Item: model.MediaItem{ID: "a"}, Status: model.StatusPending}}

	s.ApplyProgress(model.ProgressEvent{Item: model.MediaItem{ID: "a"}, Status: model.StatusFailed})
	if s.Items[0].Status != model.StatusPending {
		t.Fatal("a failed progress event must not set a terminal status")
	}
	s.ApplyProgress(model.ProgressEvent{Item: model.MediaItem{ID: "a"}, Status: model.StatusCompleted, Bytes: 5, Total: 10})
	if s.Items[0].Status != model.StatusDownloading || s.Items[0].Total != 10 {
		t.Fatalf("completed progress must read as downloading until done, got %+v", s.Items[0])
	}
	s.Items[0].Status = model.StatusCancelled
	s.ApplyDone("a", errors.New("cancelled"))
	if s.Items[0].Status != model.StatusCancelled {
		t.Fatal("a cancelled item must not be downgraded to failed")
	}
	s.ApplyProgress(model.ProgressEvent{Item: model.MediaItem{ID: "a"}, Status: model.StatusDownloading})
	if s.Items[0].Status != model.StatusCancelled {
		t.Fatal("stale progress must not resurrect a cancelled item")
	}
}

func TestDrainBuffersLatestEventPerItem(t *testing.T) {
	dl := newFakeDownloader()
	s := New(func(context.Context) ([]model.MediaItem, error) { return items("a.mp4"), nil }, dl, t.TempDir(), "test")
	tasks := s.Apply(s.Exec(s.Start()[0]))
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d", len(tasks))
	}
	item := s.Items[0].Item
	dl.ch <- model.ProgressEvent{Item: item, Status: model.StatusDownloading, Bytes: 10}
	dl.ch <- model.ProgressEvent{Item: item, Status: model.StatusDownloading, Bytes: 40}
	deadline := time.Now().Add(time.Second)
	for !s.HasPending() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	if !s.Refresh() {
		t.Fatal("an active run must keep refreshing")
	}
	if s.Items[0].Bytes != 40 {
		t.Fatalf("bytes = %d, want the latest event's 40", s.Items[0].Bytes)
	}
	s.Shutdown()
}

func TestStatsAndETA(t *testing.T) {
	s := New(nil, nil, t.TempDir(), "test")
	s.Phase = PhaseDownload
	s.Items = []Item{
		{Item: model.MediaItem{Size: 100}, Status: model.StatusDownloading, Bytes: 50, Total: 100, Speed: 10},
		{Item: model.MediaItem{Size: 50}, Status: model.StatusPending},
		{Item: model.MediaItem{Size: 20}, Status: model.StatusCompleted},
	}
	st := s.Stats()
	if st.Total != 3 || st.Active != 1 || st.Pending != 1 || st.Completed != 1 || st.DoneBytes != 70 || st.TotalBytes != 170 {
		t.Fatalf("stats = %+v", st)
	}
	if st.ETA != 10*time.Second {
		t.Fatalf("eta = %v, want 10s", st.ETA)
	}
	s.Items = append(s.Items, Item{Status: model.StatusPending})
	if st := s.Stats(); st.ETA != 0 || st.Unknown != 1 {
		t.Fatalf("unknown sizes must hide the ETA: %+v", st)
	}
}

func TestFindNextWrapsAndSkipsHeaders(t *testing.T) {
	s := New(nil, nil, t.TempDir(), "test")
	s.Items = []Item{
		{Header: true, Album: "a"},
		{Item: model.MediaItem{Title: "Cat.mp4"}, Status: model.StatusFailed},
		{Item: model.MediaItem{Title: "dog.mp4"}},
	}
	v := s.Visible()
	if row := s.FindNext(v, 2, MatchTitle("cat")); row != 1 {
		t.Fatalf("FindNext(cat) = %d, want 1 (wrapped)", row)
	}
	if row := s.FindNext(v, 1, IsFailed); row != 1 {
		t.Fatalf("FindNext(failed) = %d, want 1", row)
	}
	if row := s.FindNext(v, 0, MatchTitle("")); row != -1 {
		t.Fatalf("an empty query matches nothing, got %d", row)
	}
	s.Filter = FilterFailed
	if v := s.Visible(); len(v) != 2 {
		t.Fatalf("failed filter = %v, want header + failed item", v)
	}
}

func TestRowsQueuePositions(t *testing.T) {
	s := New(nil, nil, t.TempDir(), "test")
	s.Items = []Item{
		{Header: true},
		{Status: model.StatusPending},
		{Status: model.StatusCompleted},
		{Status: model.StatusPending},
	}
	rows := s.Rows(s.Visible(), 0, 10)
	if len(rows) != 4 || rows[1].QueuePos != 1 || rows[3].QueuePos != 2 || rows[0].Count != 3 || rows[0].Done != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows := s.Rows(s.Visible(), 3, 4); rows[0].QueuePos != 2 {
		t.Fatalf("a windowed row must still count earlier pending items, got %d", rows[0].QueuePos)
	}
}

func TestIndexOfIsIncremental(t *testing.T) {
	s := New(nil, nil, t.TempDir(), "test")
	s.Items = []Item{{Item: model.MediaItem{URL: "a"}}, {Item: model.MediaItem{URL: "a"}}}
	s.IndexOf("a")
	if allocs := testing.AllocsPerRun(10, func() { s.IndexOf("a") }); allocs > 0 {
		t.Fatalf("IndexOf allocated %.2f times per no-op call; expected incremental indexing", allocs)
	}
	s.Items = append(s.Items, Item{Header: true}, Item{Item: model.MediaItem{URL: "b"}})
	if i, ok := s.IndexOf("b"); !ok || i != 3 {
		t.Fatalf("IndexOf(b) = %d, %v; want 3", i, ok)
	}
}

func TestCancelAllAndShutdown(t *testing.T) {
	dl := newFakeDownloader()
	s := New(nil, dl, t.TempDir(), "test")
	s.Items = []Item{{Item: model.MediaItem{URL: "a"}, Status: model.StatusPending}, {Status: model.StatusCompleted}}
	if !s.CancelAll() || s.Items[0].Status != model.StatusCancelled || len(dl.cancelled) != 1 {
		t.Fatalf("CancelAll: %+v cancelled=%d", s.Items, len(dl.cancelled))
	}
	s.Shutdown()
	if dl.shutdowns != 1 {
		t.Fatalf("shutdowns = %d", dl.shutdowns)
	}
}

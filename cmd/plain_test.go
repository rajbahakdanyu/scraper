package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

// fakePlainDownloader is a dashboard.Downloader fake for exercising runPlain
// without any network access. It emits scripted progress events per item URL
// and returns scripted per-item errors.
type fakePlainDownloader struct {
	progress chan model.ProgressEvent
	scripted map[string][]model.ProgressEvent
	errs     map[string]error

	mu         sync.Mutex
	downloaded []model.MediaItem
	dests      []string
	// blockOnDownload makes Download hang until Shutdown is called, for
	// exercising the signal-triggered shutdown path.
	blockOnDownload bool
	shutdownCh      chan struct{}
}

func newFakePlainDownloader() *fakePlainDownloader {
	return &fakePlainDownloader{
		progress:   make(chan model.ProgressEvent, 100),
		scripted:   map[string][]model.ProgressEvent{},
		errs:       map[string]error{},
		shutdownCh: make(chan struct{}),
	}
}

func (f *fakePlainDownloader) Prepare(item model.MediaItem) {}

func (f *fakePlainDownloader) Download(item model.MediaItem, dest string) error {
	f.mu.Lock()
	f.downloaded = append(f.downloaded, item)
	f.dests = append(f.dests, dest)
	block := f.blockOnDownload
	f.mu.Unlock()
	if block {
		<-f.shutdownCh
		return &model.CancelledError{URL: item.URL}
	}
	for _, evt := range f.scripted[item.URL] {
		evt.Item = item
		f.progress <- evt
	}
	return f.errs[item.URL]
}

func (f *fakePlainDownloader) Cancel(item model.MediaItem) {}

func (f *fakePlainDownloader) Shutdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.shutdownCh:
	default:
		close(f.progress)
		close(f.shutdownCh)
	}
}

func (f *fakePlainDownloader) Progress() <-chan model.ProgressEvent { return f.progress }

func itemsScraper(items []model.MediaItem, err error) func() ([]model.MediaItem, error) {
	return func() ([]model.MediaItem, error) { return items, err }
}

func TestRunPlainRefusesSymlinkedAlbumDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "album")); err != nil {
		t.Fatal(err)
	}

	dl := newFakePlainDownloader()
	items := []model.MediaItem{{URL: "https://example.com/a", Title: "a"}, {URL: "https://example.com/b", Title: "b"}}
	err := runPlain(dl, itemsScraper(items, nil), root, "album", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "refusing to use symlinked output directory") {
		t.Fatalf("expected symlinked directory refusal, got %v", err)
	}
	if len(dl.downloaded) != 0 {
		t.Fatalf("downloads started despite symlinked directory: %d", len(dl.downloaded))
	}
}

func TestProgressLine(t *testing.T) {
	item := model.MediaItem{URL: "https://example.com/a.mp4", Title: "a.mp4"}
	tests := []struct {
		name string
		evt  model.ProgressEvent
		want string
	}{
		{
			name: "downloading with known total shows the percentage",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusDownloading, Bytes: 50, Total: 200},
			want: "downloading a.mp4 (25%)",
		},
		{
			name: "downloading percentage is capped at 100",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusDownloading, Bytes: 300, Total: 200},
			want: "downloading a.mp4 (100%)",
		},
		{
			name: "downloading with unknown total omits the percentage",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusDownloading, Bytes: 50},
			want: "downloading a.mp4",
		},
		{
			name: "title falls back to the URL",
			evt: model.ProgressEvent{
				Item:   model.MediaItem{URL: "https://example.com/b.mp4"},
				Status: model.StatusDownloading, Bytes: 1, Total: 2,
			},
			want: "downloading https://example.com/b.mp4 (50%)",
		},
		{
			name: "completed uses the saved filename",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusCompleted, Filename: "/out/a.mp4"},
			want: "completed /out/a.mp4",
		},
		{
			name: "completed without a filename falls back to the title",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusCompleted},
			want: "completed a.mp4",
		},
		{
			name: "failed events print nothing (the real error comes from Download)",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusFailed},
			want: "",
		},
		{
			name: "cancelled events print nothing",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusCancelled},
			want: "",
		},
		{
			name: "pending events print nothing",
			evt:  model.ProgressEvent{Item: item, Status: model.StatusPending},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := progressLine(tt.evt); got != tt.want {
				t.Errorf("progressLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlainPrinterDeduplicatesUnchangedPercent(t *testing.T) {
	item := model.MediaItem{URL: "https://example.com/a.mp4", Title: "a.mp4"}
	var buf bytes.Buffer
	p := newPlainPrinter(&buf)

	evt := model.ProgressEvent{Item: item, Status: model.StatusDownloading, Bytes: 10, Total: 100}
	p.event(evt)
	p.event(evt) // same percentage as the last printed line: not repeated
	p.event(model.ProgressEvent{Item: item, Status: model.StatusDownloading, Bytes: 20, Total: 100})
	p.event(model.ProgressEvent{Item: item, Status: model.StatusCompleted, Filename: "/out/a.mp4"})

	want := "downloading a.mp4 (10%)\ndownloading a.mp4 (20%)\ncompleted /out/a.mp4\n"
	if buf.String() != want {
		t.Errorf("printed %q, want %q", buf.String(), want)
	}
}

func TestPlainPrinterTracksItemsIndependently(t *testing.T) {
	a := model.MediaItem{URL: "https://example.com/a.mp4", Title: "a.mp4"}
	b := model.MediaItem{URL: "https://example.com/b.mp4", Title: "b.mp4"}
	var buf bytes.Buffer
	p := newPlainPrinter(&buf)

	p.event(model.ProgressEvent{Item: a, Status: model.StatusDownloading, Bytes: 10, Total: 100})
	p.event(model.ProgressEvent{Item: b, Status: model.StatusDownloading, Bytes: 10, Total: 100})

	got := buf.String()
	if !strings.Contains(got, "downloading a.mp4 (10%)") || !strings.Contains(got, "downloading b.mp4 (10%)") {
		t.Errorf("expected both items' first lines to print, got %q", got)
	}
}

func TestRunPlainDownloadsAllItems(t *testing.T) {
	dest := t.TempDir()
	dl := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/one.mp4", Title: "one.mp4"},
		{URL: "https://example.com/two.mp4", Title: "two.mp4"},
	}
	dl.scripted["https://example.com/one.mp4"] = []model.ProgressEvent{
		{Status: model.StatusDownloading, Bytes: 512, Total: 1024},
		{Status: model.StatusCompleted, Filename: "one.mp4"},
	}

	var buf bytes.Buffer
	err := runPlain(dl, itemsScraper(items, nil), dest, "album", &buf)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(dl.downloaded) != 2 {
		t.Errorf("expected 2 downloads, got %d", len(dl.downloaded))
	}
	// Multiple items with a slug download into the slug subfolder, the
	// same layout the dashboard produces.
	albumDest := filepath.Join(dest, "album")
	for _, got := range dl.dests {
		if got != albumDest {
			t.Errorf("download dest = %q, want %q", got, albumDest)
		}
	}
	if info, statErr := os.Stat(albumDest); statErr != nil || !info.IsDir() {
		t.Errorf("expected the album subfolder to be created, stat err: %v", statErr)
	}

	out := buf.String()
	for _, want := range []string{"downloading one.mp4 (50%)", "completed one.mp4"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got: %q", want, out)
		}
	}
}

func TestRunPlainSingleItemUsesDestDirectly(t *testing.T) {
	dest := t.TempDir()
	dl := newFakePlainDownloader()
	items := []model.MediaItem{{URL: "https://example.com/one.mp4", Title: "one.mp4"}}

	var buf bytes.Buffer
	if err := runPlain(dl, itemsScraper(items, nil), dest, "album", &buf); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(dl.dests) != 1 || dl.dests[0] != dest {
		t.Errorf("single item dests = %v, want [%q]", dl.dests, dest)
	}
}

func TestRunPlainFailsWhenAnyDownloadFails(t *testing.T) {
	dest := t.TempDir()
	dl := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/good.mp4", Title: "good.mp4"},
		{URL: "https://example.com/bad.mp4", Title: "bad.mp4"},
	}
	dl.errs["https://example.com/bad.mp4"] = fmt.Errorf("boom")
	dl.scripted["https://example.com/good.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCompleted, Filename: "good.mp4"},
	}

	var buf bytes.Buffer
	err := runPlain(dl, itemsScraper(items, nil), dest, "album", &buf)
	if err == nil {
		t.Fatal("expected a non-nil error so the process exits non-zero when any download fails")
	}

	out := buf.String()
	if !strings.Contains(out, "failed bad.mp4: boom") {
		t.Errorf("expected output to contain the failure line with the real error, got: %q", out)
	}
	if !strings.Contains(out, "completed good.mp4") {
		t.Errorf("expected the successful item to still complete, got: %q", out)
	}
}

func TestRunPlainTreatsCancelledItemsAsInterrupted(t *testing.T) {
	dl := newFakePlainDownloader()
	item := model.MediaItem{URL: "https://example.com/cancelled.mp4", Title: "cancelled.mp4"}
	dl.errs[item.URL] = &model.CancelledError{URL: item.URL}

	var buf bytes.Buffer
	if err := runPlain(dl, itemsScraper([]model.MediaItem{item}, nil), t.TempDir(), "", &buf); err != nil {
		t.Fatalf("expected cancellation not to fail the run, got: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "failed cancelled.mp4") {
		t.Fatalf("cancelled item was reported as failed: %q", out)
	}
	if !strings.Contains(out, "interrupted: cancelled.mp4\n") {
		t.Errorf("expected interrupted line, got: %q", out)
	}
}

type blockingBatchDownloader struct {
	progress   chan model.ProgressEvent
	allowError chan struct{}
}

func (d *blockingBatchDownloader) Prepare(model.MediaItem) {}
func (d *blockingBatchDownloader) Download(item model.MediaItem, _ string) error {
	d.progress <- model.ProgressEvent{Item: item, Status: model.StatusDownloading}
	<-d.allowError
	return errors.New("download boom")
}
func (d *blockingBatchDownloader) Cancel(model.MediaItem)               {}
func (d *blockingBatchDownloader) Shutdown()                            { close(d.progress) }
func (d *blockingBatchDownloader) Progress() <-chan model.ProgressEvent { return d.progress }

type serialOutputWriter struct {
	mu        sync.Mutex
	active    bool
	overlap   bool
	lines     []string
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (w *serialOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.active {
		w.overlap = true
	}
	w.active = true
	w.lines = append(w.lines, string(p))
	if strings.Contains(string(p), "downloading ") {
		w.startOnce.Do(func() { close(w.started) })
		<-w.release
	}
	w.active = false
	w.mu.Unlock()
	return len(p), nil
}

func (w *serialOutputWriter) hasOverlap() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overlap
}

func (w *serialOutputWriter) output() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.lines...)
}

func TestRunBatchPlainSerializesProgressAndBatchOutput(t *testing.T) {
	dl := &blockingBatchDownloader{
		progress:   make(chan model.ProgressEvent, 1),
		allowError: make(chan struct{}),
	}
	scraper := &fakeBatchJSONScraper{items: []model.MediaItem{{URL: "https://example.com/item", Title: "item"}}}
	albums := []dashboard.DashAlbumURL{{URL: "https://example.com", Slug: "album", Dest: t.TempDir(), Scraper: scraper}}
	writer := &serialOutputWriter{started: make(chan struct{}), release: make(chan struct{})}

	done := make(chan error, 1)
	go func() { done <- runBatchPlain(dl, albums, t.TempDir(), writer) }()
	<-writer.started
	close(dl.allowError)
	time.Sleep(10 * time.Millisecond)
	close(writer.release)

	if err := <-done; err == nil {
		t.Fatal("expected the scripted download failure")
	}
	if writer.hasOverlap() {
		t.Fatal("batch output writes overlapped")
	}
	for _, line := range writer.output() {
		if !strings.HasSuffix(line, "\n") {
			t.Errorf("output write was not a complete line: %q", line)
		}
	}
}

func TestRunPlainPropagatesScrapeError(t *testing.T) {
	dl := newFakePlainDownloader()
	wantErr := fmt.Errorf("scrape boom")

	var buf bytes.Buffer
	err := runPlain(dl, itemsScraper(nil, wantErr), t.TempDir(), "album", &buf)
	if err != wantErr {
		t.Errorf("expected the scrape error to propagate, got: %v", err)
	}
	if len(dl.downloaded) != 0 {
		t.Errorf("expected no downloads after a scrape error, got %d", len(dl.downloaded))
	}
}

func TestRunPlainNoItems(t *testing.T) {
	dl := newFakePlainDownloader()

	var buf bytes.Buffer
	err := runPlain(dl, itemsScraper(nil, nil), t.TempDir(), "album", &buf)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "No items found") {
		t.Errorf("expected output to mention no items found, got: %q", buf.String())
	}
}

func TestRunPlainTerminatesOnClosedProgressChannel(t *testing.T) {
	// A closed progress channel (e.g. once graceful shutdown lands) must
	// end the consumer cleanly instead of deadlocking or panicking.
	dest := t.TempDir()
	dl := newFakePlainDownloader()
	close(dl.progress)
	items := []model.MediaItem{{URL: "https://example.com/one.mp4", Title: "one.mp4"}}

	var buf bytes.Buffer
	if err := runPlain(dl, itemsScraper(items, nil), dest, "album", &buf); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(dl.downloaded) != 1 {
		t.Errorf("expected the item to be downloaded, got %d downloads", len(dl.downloaded))
	}
}

func TestRunPlainShutsDownOnInterrupt(t *testing.T) {
	// Safety net: with at least one Notify registered for SIGINT, the
	// self-sent signal below can never terminate the test process, even if
	// it arrives before runPlain's own watcher is ready.
	safety := make(chan os.Signal, 1)
	signal.Notify(safety, os.Interrupt)
	defer signal.Stop(safety)

	dest := t.TempDir()
	dl := newFakePlainDownloader()
	dl.blockOnDownload = true
	items := []model.MediaItem{{URL: "https://example.com/one.mp4", Title: "one.mp4"}}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runPlain(dl, itemsScraper(items, nil), dest, "", &bytes.Buffer{})
	}()

	// Wait until the download is genuinely in flight, so the interrupt
	// exercises the shutdown path rather than racing startup.
	startDeadline := time.Now().Add(2 * time.Second)
	for {
		dl.mu.Lock()
		started := len(dl.downloaded) == 1
		dl.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(startDeadline) {
			t.Fatal("download never started")
		}
		time.Sleep(time.Millisecond)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}

	// The blocked download only returns once the signal watcher triggers
	// Shutdown, so a prompt return proves the wiring.
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected cancellation-only run to succeed, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runPlain did not return after SIGINT")
	}
}

func TestRunPlainAssignsIDsLikeTheDashboard(t *testing.T) {
	// Items without an ID get a positional ID before Prepare/Download, so
	// duplicate URLs within one run stay distinct (mirrors the dashboard).
	dest := t.TempDir()
	dl := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/dup.mp4", Title: "dup.mp4"},
		{URL: "https://example.com/dup.mp4", Title: "dup.mp4"},
	}

	var buf bytes.Buffer
	if err := runPlain(dl, itemsScraper(items, nil), dest, "album", &buf); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(dl.downloaded) != 2 {
		t.Fatalf("expected 2 downloads, got %d", len(dl.downloaded))
	}
	seen := map[string]bool{}
	for _, item := range dl.downloaded {
		if item.ID == "" {
			t.Error("expected an assigned ID, got empty")
		}
		if seen[item.Key()] {
			t.Errorf("expected distinct keys for duplicate URLs, saw %q twice", item.Key())
		}
		seen[item.Key()] = true
	}
}

func TestRunJSONProducesJSONLines(t *testing.T) {
	fd := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}
	fd.scripted["https://example.com/a.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCompleted, Filename: "/tmp/a.mp4"},
	}
	fd.scripted["https://example.com/b.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCompleted, Filename: "/tmp/b.mp4"},
	}

	var buf bytes.Buffer
	err := runJSON(fd, itemsScraper(items, nil), t.TempDir(), "album", "test", "https://example.com", "", "", &buf)
	if err != nil {
		t.Fatalf("runJSON failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 JSON lines (album_start, 2 items, summary), got %d:\n%s", len(lines), buf.String())
	}

	var firstEvent struct{ Event string }
	if err := json.Unmarshal([]byte(lines[0]), &firstEvent); err != nil || firstEvent.Event != "album_start" {
		t.Errorf("expected album_start as first line, got: %s", lines[0])
	}

	var lastEvent struct{ Event string }
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &lastEvent); err != nil || lastEvent.Event != "summary" {
		t.Errorf("expected summary as last line, got: %s", lines[len(lines)-1])
	}

	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Errorf("invalid JSON line: %s", line)
		}
	}
}

func TestRunJSONWithFailedItems(t *testing.T) {
	fd := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}
	fd.errs["https://example.com/a.mp4"] = fmt.Errorf("HTTP 404")

	var buf bytes.Buffer
	_ = runJSON(fd, itemsScraper(items, nil), t.TempDir(), "album", "test", "https://example.com", "", "", &buf)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var itemEvent struct {
		Event  string
		Status string
		Error  string
	}
	found := false
	for _, line := range lines {
		if err := json.Unmarshal([]byte(line), &itemEvent); err == nil && itemEvent.Event == "item" {
			found = true
			if itemEvent.Status != "failed" {
				t.Errorf("expected failed status, got %s", itemEvent.Status)
			}
			if !strings.Contains(itemEvent.Error, "HTTP 404") {
				t.Errorf("expected HTTP 404 error, got %s", itemEvent.Error)
			}
		}
	}
	if !found {
		t.Errorf("expected an item event, lines:\n%s", buf.String())
	}
}

func TestRunJSONHandlesNoItems(t *testing.T) {
	fd := newFakePlainDownloader()
	var buf bytes.Buffer
	err := runJSON(fd, itemsScraper(nil, nil), t.TempDir(), "", "test", "https://example.com", "", "", &buf)
	if err != nil {
		t.Fatalf("expected no error for empty result, got: %v", err)
	}
	if !strings.Contains(buf.String(), "no media items found") {
		t.Errorf("expected no-items error event, got: %s", buf.String())
	}
}

func TestRunJSONShutsDownProgress(t *testing.T) {
	fd := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100, ID: "1"},
	}
	fd.scripted["https://example.com/a.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCompleted, Filename: "/tmp/a.mp4"},
	}
	fd.blockOnDownload = false

	var buf bytes.Buffer
	err := runJSON(fd, itemsScraper(items, nil), t.TempDir(), "album", "test", "https://example.com", "", "", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Shutdown should have been called after downloads finished.
	fd.mu.Lock()
	defer fd.mu.Unlock()
	select {
	case <-fd.shutdownCh:
		// Shutdown was called — progress channel would close after this
	default:
		t.Fatal("Shutdown was NOT called after runJSON completed")
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (album_start, item, summary), got %d:\n%s", len(lines), buf.String())
	}
}

type fakeBatchJSONScraper struct {
	items []model.MediaItem
	err   error
}

func (f *fakeBatchJSONScraper) Name() string { return "fake" }
func (f *fakeBatchJSONScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	return f.items, f.err
}

type interruptingBatchScraper struct {
	started  chan struct{}
	complete int
}

func (s *interruptingBatchScraper) Name() string { return "interrupting" }

func (s *interruptingBatchScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	if s.complete == 0 {
		s.complete++
		return []model.MediaItem{{URL: "https://example.com/first.mp4", Title: "first.mp4"}}, nil
	}
	close(s.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRunBatchPlainInterruptStopsAfterFirstScrape(t *testing.T) {
	safety := make(chan os.Signal, 1)
	signal.Notify(safety, os.Interrupt)
	defer signal.Stop(safety)

	dl := newFakePlainDownloader()
	scraper := &interruptingBatchScraper{started: make(chan struct{})}
	albums := []dashboard.DashAlbumURL{
		{URL: "https://example.com/one", Slug: "one", Dest: t.TempDir(), Scraper: scraper},
		{URL: "https://example.com/two", Slug: "two", Dest: t.TempDir(), Scraper: scraper},
		{URL: "https://example.com/three", Slug: "three", Dest: t.TempDir(), Scraper: scraper},
	}

	done := make(chan error, 1)
	var out bytes.Buffer
	go func() { done <- runBatchPlain(dl, albums, t.TempDir(), &out) }()

	select {
	case <-scraper.started:
	case <-time.After(2 * time.Second):
		t.Fatal("second scrape did not start")
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean batch interruption, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("batch did not stop after SIGINT")
	}
	if scraper.complete != 1 {
		t.Fatalf("expected only the first album to scrape successfully, got %d", scraper.complete)
	}
	if strings.Contains(out.String(), "failed: context canceled") {
		t.Fatalf("cancellation was reported as a scrape failure: %s", out.String())
	}
}

func TestRunBatchJSONProducesJSONLines(t *testing.T) {
	fd := newFakePlainDownloader()
	scraper := &fakeBatchJSONScraper{items: []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 200},
	}}
	fd.scripted["https://example.com/a.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCompleted, Filename: "/tmp/a.mp4"},
	}
	fd.scripted["https://example.com/b.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCompleted, Filename: "/tmp/b.mp4"},
	}

	albums := []dashboard.DashAlbumURL{
		{Site: "test", URL: "https://example.com", Slug: "album1", Dest: t.TempDir(), Scraper: scraper},
		{Site: "test", URL: "https://example.com", Slug: "album2", Dest: t.TempDir(), Scraper: scraper},
	}

	var buf bytes.Buffer
	err := runBatchJSON(fd, albums, "/tmp", "", "", &buf)
	if err != nil {
		t.Fatalf("runBatchJSON failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 6 {
		t.Fatalf("expected at least 6 JSON lines (2x album_start, 4 items, summary), got %d:\n%s", len(lines), buf.String())
	}

	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Errorf("invalid JSON line: %s", line)
		}
		var event struct {
			Event  string `json:"event"`
			Status string `json:"status"`
			Path   string `json:"path"`
		}
		if err := json.Unmarshal([]byte(line), &event); err == nil && event.Event == "item" && event.Status == "completed" && event.Path == "" {
			t.Errorf("completed item has empty path: %s", line)
		}
	}

	var lastEvent struct{ Event string }
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &lastEvent); err != nil || lastEvent.Event != "summary" {
		t.Errorf("expected summary as last line, got: %s", lines[len(lines)-1])
	}
}

func TestRunBatchJSONWithFailedAlbum(t *testing.T) {
	fd := newFakePlainDownloader()
	scraper := &fakeBatchJSONScraper{items: nil, err: errors.New("parse error")}

	albums := []dashboard.DashAlbumURL{
		{Site: "test", URL: "https://example.com", Slug: "fail", Dest: t.TempDir(), Scraper: scraper},
	}

	var buf bytes.Buffer
	_ = runBatchJSON(fd, albums, "/tmp", "", "", &buf)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	found := false
	for _, line := range lines {
		var evt struct{ Event string }
		if err := json.Unmarshal([]byte(line), &evt); err == nil && evt.Event == "album_error" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected album_error event, got:\n%s", buf.String())
	}
}

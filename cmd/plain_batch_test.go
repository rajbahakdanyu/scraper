package cmd

import (
	"bytes"
	"context"
	"errors"
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

type blockingBatchDownloader struct {
	progress   chan model.ProgressEvent
	allowError chan struct{}
}

func (d *blockingBatchDownloader) Prepare(model.MediaItem) {}

func (d *blockingBatchDownloader) Download(item model.MediaItem, _ string) (string, error) {
	d.progress <- model.ProgressEvent{Item: item, Status: model.StatusDownloading}
	<-d.allowError
	return "", errors.New("download boom")
}

func (d *blockingBatchDownloader) Cancel(model.MediaItem) {}

func (d *blockingBatchDownloader) Shutdown() { close(d.progress) }

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

type overlapBatchScraper struct {
	secondStarted chan struct{}
}

func (s *overlapBatchScraper) Name() string { return "overlap" }

func (s *overlapBatchScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	if strings.HasSuffix(url, "/two") {
		close(s.secondStarted)
	}
	return []model.MediaItem{{URL: url + ".jpg", Title: url}}, nil
}

type overlapBatchDownloader struct {
	*fakePlainDownloader
	firstStarted chan struct{}
	release      chan struct{}
	startOnce    sync.Once
}

func (d *overlapBatchDownloader) Download(item model.MediaItem, dest string) (string, error) {
	d.startOnce.Do(func() { close(d.firstStarted) })
	<-d.release
	return "", nil
}

func newOverlapBatchDownloader() *overlapBatchDownloader {
	return &overlapBatchDownloader{
		fakePlainDownloader: newFakePlainDownloader(),
		firstStarted:        make(chan struct{}),
		release:             make(chan struct{}),
	}
}

func TestRunBatchPlainOverlapsNextScrapeWithCurrentDownload(t *testing.T) {
	dl := newOverlapBatchDownloader()
	scraper := &overlapBatchScraper{secondStarted: make(chan struct{})}
	albums := []dashboard.DashAlbumURL{
		{URL: "https://example.com/one", Slug: "one", Dest: t.TempDir(), Scraper: scraper},
		{URL: "https://example.com/two", Slug: "two", Dest: t.TempDir(), Scraper: scraper},
	}
	done := make(chan error, 1)
	go func() { done <- runBatchPlain(dl, albums, t.TempDir(), &bytes.Buffer{}) }()
	select {
	case <-dl.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first download did not start")
	}
	select {
	case <-scraper.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second scrape did not overlap the first download")
	}
	close(dl.release)
	if err := <-done; err != nil {
		t.Fatalf("batch failed: %v", err)
	}
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

func TestRunBatchPlainPrintsAlbumSections(t *testing.T) {
	dl := newFakePlainDownloader()
	scraper := &fakeBatchJSONScraper{items: []model.MediaItem{{URL: "https://example.com/item", Title: "item"}}}
	albums := []dashboard.DashAlbumURL{
		{URL: "https://example.com/one", Slug: "one", Dest: t.TempDir(), Scraper: scraper},
		{URL: "https://example.com/two", Slug: "two", Dest: t.TempDir(), Scraper: scraper},
	}

	var out bytes.Buffer
	if err := runBatchPlain(dl, albums, t.TempDir(), &out); err != nil {
		t.Fatalf("runBatchPlain failed: %v", err)
	}
	for _, slug := range []string{"[one]", "[two]"} {
		if !strings.Contains(out.String(), slug) {
			t.Errorf("expected section header %q, got:\n%s", slug, out.String())
		}
	}
}

func TestRunBatchPlainReportsScrapeFailures(t *testing.T) {
	dl := newFakePlainDownloader()
	scrapeErr := errors.New("parse error")
	albums := []dashboard.DashAlbumURL{{
		URL:     "https://example.com/fail",
		Slug:    "fail",
		Dest:    t.TempDir(),
		Scraper: &fakeBatchJSONScraper{err: scrapeErr},
	}}

	var out bytes.Buffer
	err := runBatchPlain(dl, albums, t.TempDir(), &out)
	if err == nil {
		t.Fatal("expected a partial batch error")
	}
	if !strings.Contains(out.String(), "failed: parse error") {
		t.Errorf("expected per-album scrape failure line, got:\n%s", out.String())
	}
	var partial *model.PartialError
	if !errors.As(err, &partial) || len(partial.Errors) != 1 || !errors.Is(partial, scrapeErr) {
		t.Fatalf("expected partial error containing scrape error, got %v", err)
	}
}

func TestRunBatchPlainReturnsPartialErrorAfterMixedAlbumResults(t *testing.T) {
	dl := newFakePlainDownloader()
	scrapeErr := errors.New("second album failed")
	albums := []dashboard.DashAlbumURL{
		{
			URL:     "https://example.com/good",
			Slug:    "good",
			Dest:    t.TempDir(),
			Scraper: &fakeBatchJSONScraper{items: []model.MediaItem{{URL: "https://example.com/item", Title: "item"}}},
		},
		{URL: "https://example.com/bad", Slug: "bad", Dest: t.TempDir(), Scraper: &fakeBatchJSONScraper{err: scrapeErr}},
	}

	var out bytes.Buffer
	err := runBatchPlain(dl, albums, t.TempDir(), &out)
	if err == nil {
		t.Fatal("expected a partial batch error")
	}
	if !strings.Contains(out.String(), "[good]") || !strings.Contains(out.String(), "[bad]") {
		t.Fatalf("expected both album sections, got:\n%s", out.String())
	}
	var partial *model.PartialError
	if !errors.As(err, &partial) || len(partial.Errors) != 1 || !errors.Is(partial, scrapeErr) {
		t.Fatalf("expected one scrape failure in partial error, got %v", err)
	}
}

func TestRunBatchPlainSkipsSymlinkedAlbumAndContinues(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	dl := newFakePlainDownloader()
	scraper := &mockScraper{items: []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
	}}
	albums := []dashboard.DashAlbumURL{
		{URL: "https://example.com/1", Slug: "linked", Dest: filepath.Join(root, "linked"), Scraper: scraper},
		{URL: "https://example.com/2", Slug: "good", Dest: filepath.Join(root, "good"), Scraper: scraper},
	}

	done := make(chan error, 1)
	var buf bytes.Buffer
	go func() { done <- runBatchPlain(dl, albums, root, &buf) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected partial error for the symlink refusal, got nil")
		}
		if !strings.Contains(err.Error(), "refusing to use symlinked output directory") {
			t.Fatalf("expected symlink refusal in error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runBatchPlain deadlocked after symlinked album dir")
	}

	out := buf.String()
	if !strings.Contains(out, "refusing to use symlinked output directory") {
		t.Errorf("expected symlink warning, got: %q", out)
	}
	if len(dl.dests) == 0 || !strings.HasSuffix(dl.dests[0], filepath.Join(root, "good")) {
		t.Errorf("second album should still download, dests=%v", dl.dests)
	}
}

package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func runBatch(opts globalOptions, entries []batchEntry) error {
	cfg, err := configLoader(opts)
	if err != nil {
		return err
	}
	return runBatchWithConfig(cfg, opts, entries)
}

// fakeDryRunScraper is a minimal model.Scraper for exercising runDryRun
// without any network access.
type fakeDryRunScraper struct {
	name  string
	items []model.MediaItem
	err   error
}

func (f *fakeDryRunScraper) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fake"
}

func (f *fakeDryRunScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	return f.items, f.err
}

// captureStdout redirects os.Stdout for the duration of f and returns
// everything written to it.
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// captureStderr redirects os.Stderr for the duration of f and returns
// everything written to it.
func captureStderr(f func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// emptyTestConfig writes a minimal empty config file and returns its path.
func emptyTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type recordingDelaySleeper struct{ delays []time.Duration }

func (s *recordingDelaySleeper) Sleep(time.Duration) {}

func (s *recordingDelaySleeper) SleepCtx(_ context.Context, d time.Duration) error {
	s.delays = append(s.delays, d)
	return nil
}

func restoreBatchFactories(t *testing.T) {
	oldRegistry := batchScraperRegistry
	oldGeneric := batchGenericDownloader
	oldYouTubeScraper := batchYouTubeScraper
	oldYouTube := batchYouTubeDownloader
	t.Cleanup(func() {
		batchScraperRegistry = oldRegistry
		batchGenericDownloader = oldGeneric
		batchYouTubeScraper = oldYouTubeScraper
		batchYouTubeDownloader = oldYouTube
	})
}

type mockScraper struct {
	name  string
	items []model.MediaItem
	err   error
}

func (m *mockScraper) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

func (m *mockScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	return m.items, m.err
}

// fakePlainDownloader is a model.Downloader fake for exercising runPlain
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

func (f *fakePlainDownloader) Download(item model.MediaItem, dest string) (string, error) {
	f.mu.Lock()
	f.downloaded = append(f.downloaded, item)
	f.dests = append(f.dests, dest)
	block := f.blockOnDownload
	f.mu.Unlock()
	if block {
		<-f.shutdownCh
		return "", &model.CancelledError{URL: item.URL}
	}
	var completedPath string
	for _, evt := range f.scripted[item.URL] {
		evt.Item = item
		f.progress <- evt
		if evt.Status == model.StatusCompleted {
			completedPath = evt.Filename
		}
	}
	return completedPath, f.errs[item.URL]
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

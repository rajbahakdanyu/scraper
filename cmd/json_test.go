package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestRunDryRunJSONProducesValidJSON(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{Title: "one.mp4", Size: 1024 * 1024, URL: "https://example.com/one"},
		{Title: "two.jpg", Size: 2 * 1024 * 1024, URL: "https://example.com/two"},
	}}

	var err error
	out := captureStdout(func() {
		err = runDryRunJSONWithDiskCheck(scraper, "https://example.com/a", "example", "", os.Stdout, "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, `"dryRun"`) || !strings.Contains(out, `"one.mp4"`) || !strings.Contains(out, `"two.jpg"`) {
		t.Errorf("expected JSON output with items, got: %q", out)
	}
	if !strings.Contains(out, `"albums"`) || !strings.Contains(out, `"summary"`) {
		t.Errorf("expected albums and summary in JSON, got: %q", out)
	}
}

func TestRunDryRunJSONEmptyItems(t *testing.T) {
	scraper := &fakeDryRunScraper{items: nil}
	out := captureStdout(func() {
		_ = runDryRunJSONWithDiskCheck(scraper, "https://example.com/a", "example", "", os.Stdout, "", false)
	})
	if !strings.Contains(out, `"dryRun"`) && !strings.Contains(out, `"summary"`) {
		t.Errorf("expected dry run JSON with summary, got: %q", out)
	}
}

func TestRunDryRunJSONEmptyScrapeIsInformational(t *testing.T) {
	cfg, err := config.Load(emptyTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	scraper := app.PreviewFilters(&fakeDryRunScraper{}, cfg, cliHooks)

	var buf bytes.Buffer
	if err := runDryRunJSONWithDiskCheck(scraper, "https://cyberdrop.cr/a/empty", "cyberdrop", "", &buf, t.TempDir(), false); err != nil {
		t.Fatalf("dry-run of an empty scrape must exit 0, got %v", err)
	}
	if strings.Contains(buf.String(), `"error"`) {
		t.Fatalf("expected an empty dry-run summary without an error event, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"dryRun"`) {
		t.Fatalf("expected a dry-run document, got: %s", buf.String())
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

func TestRunJSONWritesRetryListInOutputRoot(t *testing.T) {
	fd := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", PageURL: "https://example.com/f/a"},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", PageURL: "https://example.com/f/b"},
	}
	fd.errs["https://example.com/a.mp4"] = fmt.Errorf("HTTP 404")

	dest := t.TempDir()
	var buf bytes.Buffer
	_ = runJSON(fd, itemsScraper(items, nil), dest, "album", "test", "https://example.com/a/album", "", "", &buf)

	matches, _ := filepath.Glob(filepath.Join(dest, "scraper-failed-*.txt"))
	if len(matches) != 1 {
		t.Fatalf("expected the retry list in the output root %s, got %v\n%s", dest, matches, buf.String())
	}
}

func TestRunJSONWithFailedItems(t *testing.T) {
	fd := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100, PageURL: "https://example.com/f/a"},
	}
	fd.errs["https://example.com/a.mp4"] = fmt.Errorf("HTTP 404")

	dest := t.TempDir()
	var buf bytes.Buffer
	_ = runJSON(fd, itemsScraper(items, nil), dest, "album", "test", "https://example.com", "", "", &buf)

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

	var summaryEvent struct {
		Event     string
		RetryFile string
	}
	for _, line := range lines {
		_ = json.Unmarshal([]byte(line), &summaryEvent)
		if summaryEvent.Event == "summary" {
			break
		}
	}
	if summaryEvent.Event != "summary" || summaryEvent.RetryFile == "" {
		t.Fatalf("expected a summary event with retryFile, got: %s", buf.String())
	}
	if _, err := os.Stat(summaryEvent.RetryFile); err != nil {
		t.Fatalf("retryFile does not exist: %v", err)
	}
	data, err := os.ReadFile(summaryEvent.RetryFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://example.com/f/a\thttps://example.com") {
		t.Fatalf("retry file missing item/album pair:\n%s", data)
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

func TestRunJSONCancelledDownloadIsNotAnError(t *testing.T) {
	dl := newFakePlainDownloader()
	cancelled := &model.CancelledError{URL: "https://example.com/a.mp4"}
	dl.errs["https://example.com/a.mp4"] = cancelled
	items := []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100}}
	dl.scripted["https://example.com/a.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCancelled},
	}

	var buf bytes.Buffer
	err := runJSON(dl, itemsScraper(items, nil), t.TempDir(), "album", "test", "https://example.com", "", "", &buf)
	if err != nil {
		t.Fatalf("cancelled download must not fail the run, got: %v", err)
	}
}

type fakeBatchJSONScraper struct {
	items []model.MediaItem
	err   error
}

func TestRunBatchJSONOverlapsNextScrapeWithCurrentDownload(t *testing.T) {
	dl := newOverlapBatchDownloader()
	scraper := &overlapBatchScraper{secondStarted: make(chan struct{})}
	albums := []model.Album{
		{Site: "test", URL: "https://example.com/one", Slug: "one", Dest: t.TempDir(), Scraper: scraper},
		{Site: "test", URL: "https://example.com/two", Slug: "two", Dest: t.TempDir(), Scraper: scraper},
	}
	done := make(chan error, 1)
	var out bytes.Buffer
	go func() { done <- runBatchJSON(dl, albums, t.TempDir(), "", "", &out) }()
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

func (f *fakeBatchJSONScraper) Name() string { return "fake" }

func (f *fakeBatchJSONScraper) Parse(ctx context.Context, url string) ([]model.MediaItem, error) {
	return f.items, f.err
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

	albums := []model.Album{
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
	var summary model.JSONEvent
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.TotalBytes != 600 {
		t.Errorf("summary totalBytes = %d, want 600", summary.TotalBytes)
	}
}

func TestRunBatchJSONRetryFileCarriesAlbumURL(t *testing.T) {
	destRoot := t.TempDir()
	fd := newFakePlainDownloader()
	scraper := &fakeBatchJSONScraper{items: []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", PageURL: "https://example.com/f/a"},
	}}
	fd.errs["https://example.com/a.mp4"] = errors.New("boom")

	albums := []model.Album{
		{Site: "test", URL: "https://example.com/a/album1", Slug: "album1", Dest: t.TempDir(), Scraper: scraper},
	}

	var buf bytes.Buffer
	_ = runBatchJSON(fd, albums, destRoot, "", "", &buf)

	matches, _ := filepath.Glob(filepath.Join(destRoot, "scraper-failed-*.txt"))
	if len(matches) != 1 {
		t.Fatalf("retry files = %v, want 1", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://example.com/f/a\thttps://example.com/a/album1") {
		t.Fatalf("retry file missing item/album pair:\n%s", data)
	}
}

func TestRunBatchJSONWithFailedAlbum(t *testing.T) {
	fd := newFakePlainDownloader()
	scraper := &fakeBatchJSONScraper{items: nil, err: errors.New("parse error")}

	albums := []model.Album{
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

func TestRunBatchJSONReportsPartialScrapeAndRetrySource(t *testing.T) {
	destRoot := t.TempDir()
	dl := newFakePlainDownloader()
	scrapeErr := errors.New("second page failed")
	scraper := &fakeBatchJSONScraper{
		items: []model.MediaItem{{URL: "https://example.com/a.mp4", Title: "a.mp4"}},
		err:   scrapeErr,
	}
	albums := []model.Album{{
		Site: "test", URL: "https://example.com/album", Slug: "album",
		Dest: t.TempDir(), Scraper: scraper,
	}}
	var buf bytes.Buffer
	err := runBatchJSON(dl, albums, destRoot, "", "", &buf)
	var partial *model.PartialError
	if !errors.As(err, &partial) || !errors.Is(err, scrapeErr) {
		t.Fatalf("expected partial error carrying scrape warning, got %v", err)
	}
	var warning, summary model.JSONEvent
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var event model.JSONEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		switch event.Event {
		case "scrape_warning":
			warning = event
		case "summary":
			summary = event
		}
	}
	if warning.Event == "" || warning.Error != scrapeErr.Error() {
		t.Fatalf("missing scrape_warning event: %+v", warning)
	}
	if summary.Event == "" || !summary.Partial || summary.RetryFile == "" {
		t.Fatalf("summary must identify partial result and retry file: %+v", summary)
	}
	data, err := os.ReadFile(summary.RetryFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://example.com/album") {
		t.Fatalf("retry file missing scrape source: %s", data)
	}
}

func TestRunJSONRefusesSymlinkedAlbumDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	dl := newFakePlainDownloader()
	items := []model.MediaItem{
		{URL: "https://example.com/a.mp4", Title: "a.mp4", Size: 100},
		{URL: "https://example.com/b.mp4", Title: "b.mp4", Size: 100},
	}

	var out bytes.Buffer
	err := runJSON(dl, itemsScraper(items, nil), root, "linked", "test", "https://example.com/1", "", "", &out)
	if err == nil {
		t.Fatal("expected an error naming the symlinked album")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink refusal in error, got: %v", err)
	}
	if len(dl.dests) != 0 {
		t.Errorf("no downloads should go through a symlinked dir, got %v", dl.dests)
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("expected an error JSON event, got: %q", out.String())
	}
}

func TestRunBatchJSONRefusesSymlinkedAlbumDir(t *testing.T) {
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
	albums := []model.Album{
		{Site: "test", URL: "https://example.com/1", Slug: "linked", Dest: filepath.Join(root, "linked"), Scraper: scraper},
	}

	var out bytes.Buffer
	err := runBatchJSON(dl, albums, root, "", "", &out)
	if err == nil {
		t.Fatal("expected a partial error naming the symlinked album")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink refusal in error, got: %v", err)
	}
	if len(dl.dests) != 0 {
		t.Errorf("no downloads should go through a symlinked dir, got %v", dl.dests)
	}
	if !strings.Contains(out.String(), "album_error") {
		t.Errorf("expected an album_error JSON event, got: %q", out.String())
	}
}

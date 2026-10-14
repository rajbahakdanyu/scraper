package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestApplyMediaFilterExtraExtensions(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://x/comic.cbz", Title: "comic.cbz"},
		{URL: "https://x/tool.exe", Title: "tool.exe"},
	}
	kept := applyMediaFilter(items, []string{"cbz"})
	if len(kept) != 1 || kept[0].Title != "comic.cbz" {
		t.Fatalf("applyMediaFilter() = %+v, want only the comic", kept)
	}
}

func TestPrintItemURLs(t *testing.T) {
	var buf bytes.Buffer
	printItemURLs(&buf, []model.MediaItem{
		{URL: "https://x/a.mp4"},
		{Title: "no-url"},
		{URL: "https://x/b.mp4"},
	})
	want := "https://x/a.mp4\nhttps://x/b.mp4\n"
	if buf.String() != want {
		t.Fatalf("printItemURLs() = %q, want %q", buf.String(), want)
	}
}

func TestFilterScraperScrapeDelay(t *testing.T) {
	prev := scrapeDelaySleeper
	sleeper := &recordingDelaySleeper{}
	scrapeDelaySleeper = sleeper
	t.Cleanup(func() { scrapeDelaySleeper = prev })

	fs := &filterScraper{
		inner:       &mockScraper{items: []model.MediaItem{{Title: "a.mp4"}}},
		scrapeDelay: 250 * time.Millisecond,
	}
	if _, err := fs.Parse(context.Background(), "https://example.com/a/x"); err != nil {
		t.Fatal(err)
	}
	if len(sleeper.delays) != 1 || sleeper.delays[0] != 250*time.Millisecond {
		t.Fatalf("delays = %v, want [250ms]", sleeper.delays)
	}
}

func TestApplyAlbumItemCap(t *testing.T) {
	items := []model.MediaItem{{Title: "a.mp4"}, {Title: "b.mp4"}, {Title: "c.mp4"}}

	var capped []model.MediaItem
	stderr := captureStderr(func() {
		capped = applyAlbumItemCap(items, 2)
	})
	if len(capped) != 2 || capped[0].Title != "a.mp4" || capped[1].Title != "b.mp4" {
		t.Fatalf("applyAlbumItemCap() = %+v, want the first two items", capped)
	}
	if !strings.Contains(stderr, "keeping first 2 of 3") {
		t.Fatalf("expected a cap warning, got %q", stderr)
	}

	if got := applyAlbumItemCap(items, 0); len(got) != 3 {
		t.Fatalf("cap 0 must be unlimited, got %d items", len(got))
	}
	if got := applyAlbumItemCap(items, 5); len(got) != 3 {
		t.Fatalf("cap above length must keep all items, got %d", len(got))
	}
}

func TestRunDryRunPropagatesParseError(t *testing.T) {
	wantErr := fmt.Errorf("boom")
	scraper := &fakeDryRunScraper{err: wantErr}

	var err error
	captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != wantErr {
		t.Errorf("expected the scraper's Parse error to propagate, got: %v", err)
	}
}

func TestRunDryRunNoItems(t *testing.T) {
	scraper := &fakeDryRunScraper{items: nil}

	var err error
	out := captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "No items found") {
		t.Errorf("expected output to mention no items found, got: %q", out)
	}
}

func TestRunDryRunSummarizesItems(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{Title: "one.mp4", Size: 1024 * 1024},
		{Title: "two.jpg", Size: 2 * 1024 * 1024},
	}}

	var err error
	out := captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	for _, want := range []string{"one.mp4", "two.jpg", "2 items", "3.0M total"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got: %q", want, out)
		}
	}
	if strings.Contains(out, "unknown size") {
		t.Errorf("expected no unknown-size items, got: %q", out)
	}
}

func TestRunDryRunReportsUnknownSizes(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{Title: "known.mp4", Size: 1024 * 1024},
		{Title: "unknown.mp4", Size: 0},
	}}

	var err error
	out := captureStdout(func() {
		err = runDryRunWithDiskCheck(scraper, "https://example.com/a", "", false)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "unknown size") {
		t.Errorf("expected the zero-size item to be reported as unknown size, got: %q", out)
	}
	if !strings.Contains(out, "1 with unknown size") {
		t.Errorf("expected the summary to count 1 unknown-size item, got: %q", out)
	}
}

func TestRunDiskCheckWarnsForMissingOutputDir(t *testing.T) {
	stderr := captureStderr(func() {
		runDiskCheck([]model.MediaItem{{Title: "big.mp4", Size: 1 << 62}}, filepath.Join(t.TempDir(), "missing"), true)
	})
	if !strings.Contains(stderr, "short") {
		t.Fatalf("a huge download into a fresh directory must warn, got %q", stderr)
	}
}

func TestRunDryRunEmptyScrapeIsInformational(t *testing.T) {
	// Dry-run is informational: a URL that scrapes zero items (wrapped
	// by the filter scraper under the default media-only config) must
	// print the empty message and exit 0, not fail the run.
	cfg, err := config.Load(emptyTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	scraper := applyDryRunFilters(&fakeDryRunScraper{}, cfg)

	out := captureStdout(func() {
		if err := runDryRunWithDiskCheck(scraper, "https://cyberdrop.cr/a/empty", t.TempDir(), false); err != nil {
			t.Errorf("dry-run of an empty scrape must exit 0, got %v", err)
		}
	})
	if !strings.Contains(out, "No items found.") {
		t.Fatalf("expected the empty message, got: %s", out)
	}
}

func TestDryRunAppliesFilters(t *testing.T) {
	scraper := &fakeDryRunScraper{items: []model.MediaItem{
		{URL: "https://cyberdrop.cr/f/1.mp4", Title: "one.mp4", Size: 100},
		{URL: "https://cyberdrop.cr/f/2.txt", Title: "two.txt", Size: 200},
	}}
	restoreBatchFactories(t)
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": scraper}, nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	errCh := make(chan error, 1)
	go func() {
		err := runBatch(globalOptions{
			configPath: emptyTestConfig(t), noTui: true, dryRun: true,
			include: []string{"*.mp4"}, includeSet: true,
		}, []batchEntry{{URL: "https://cyberdrop.cr/a/album"}})
		w.Close()
		errCh <- err
	}()
	if err := <-errCh; err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	out, _ := io.ReadAll(r)

	got := string(out)
	if strings.Contains(got, "two.txt") {
		t.Errorf("filtered item leaked into dry-run output: %q", got)
	}
	if !strings.Contains(got, "one.mp4") {
		t.Errorf("expected matching item in output: %q", got)
	}
}

func TestFilterScraperParse(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/video.mp4", Size: 1024},
		{URL: "https://cdn.example.com/image.jpg", Size: 512},
	}}
	fs := &filterScraper{
		inner:   inner,
		include: []string{"*.mp4"},
	}
	items, _ := fs.Parse(context.Background(), "https://example.com/a/test")
	if len(items) != 1 || items[0].URL != "https://cdn.example.com/video.mp4" {
		t.Fatalf("expected 1 filtered item, got %+v", items)
	}
}

func TestFilterScraperNoItems(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/video.mp4"},
	}}
	fs := &filterScraper{
		inner:   inner,
		exclude: []string{"*.mp4"},
	}
	items, _ := fs.Parse(context.Background(), "https://example.com/a/test")
	if len(items) != 0 {
		t.Fatalf("expected 0 items after filtering, got %d", len(items))
	}
}

func TestFilterScraperMediaOnly(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/video.mp4", Title: "video.mp4"},
		{URL: "https://cdn.example.com/setup.exe", Title: "setup.exe"},
		{URL: "https://cdn.example.com/set.zip", Title: "set.zip"},
		{URL: "https://cdn.example.com/page.html", Title: "page.html"},
	}}
	fs := &filterScraper{inner: inner, mediaOnly: true}

	var items []model.MediaItem
	stderr := captureStderr(func() {
		items, _ = fs.Parse(context.Background(), "https://example.com/a/test")
	})

	if len(items) != 2 || items[0].Title != "video.mp4" || items[1].Title != "set.zip" {
		t.Fatalf("expected video.mp4 and set.zip, got %+v", items)
	}
	if !strings.Contains(stderr, "2 skipped non-media") {
		t.Fatalf("expected a skip summary, got %q", stderr)
	}
}

func TestFilterScraperMediaOnlyBypassesYouTube(t *testing.T) {
	inner := &mockScraper{name: "youtube", items: []model.MediaItem{
		{URL: "https://youtube.com/watch?v=1", Title: "Some Video"},
	}}
	fs := &filterScraper{inner: inner, mediaOnly: true}

	items, err := fs.Parse(context.Background(), "https://youtube.com/playlist?list=1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected the YouTube item to be kept, got %d items", len(items))
	}
}

func TestFilterScraperMediaOnlyKeepsWorkingWithGlobs(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/a.mp4", Title: "a.mp4"},
		{URL: "https://cdn.example.com/b.zip", Title: "b.zip"},
		{URL: "https://cdn.example.com/c.exe", Title: "c.exe"},
	}}
	fs := &filterScraper{inner: inner, mediaOnly: true, include: []string{"*.zip"}}

	items, _ := fs.Parse(context.Background(), "https://example.com/a/test")
	if len(items) != 1 || items[0].Title != "b.zip" {
		t.Fatalf("expected only b.zip after media + include filters, got %+v", items)
	}
}

func TestApplyDryRunFiltersWrapsWhenOnlyMediaOnly(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{{Title: "a.mp4"}}}
	cfg := &config.Config{MediaOnly: true}

	wrapped := applyDryRunFilters(inner, cfg)
	if _, ok := wrapped.(*filterScraper); !ok {
		t.Fatalf("expected a filterScraper wrapper, got %T", wrapped)
	}
}

func TestApplyMediaFilterAllSkipped(t *testing.T) {
	var kept []model.MediaItem
	stderr := captureStderr(func() {
		kept = applyMediaFilter([]model.MediaItem{{Title: "setup.exe"}}, nil)
	})
	if len(kept) != 0 {
		t.Fatalf("expected no items, got %+v", kept)
	}
	if !strings.Contains(stderr, "1 skipped non-media") || !strings.Contains(stderr, "No media items found.") {
		t.Fatalf("expected skip summary and empty notice, got %q", stderr)
	}
}

func TestApplyMediaFilterEmptyInputIsSilent(t *testing.T) {
	stderr := captureStderr(func() {
		kept := applyMediaFilter(nil, nil)
		if len(kept) != 0 {
			t.Fatalf("expected no items, got %+v", kept)
		}
	})
	if strings.Contains(stderr, "No media items found.") {
		t.Fatalf("empty input must not print the media notice, got %q", stderr)
	}
}

func TestApplyMediaFilterSilentWhenNothingSkipped(t *testing.T) {
	stderr := captureStderr(func() {
		applyMediaFilter([]model.MediaItem{{Title: "a.mp4"}}, nil)
	})
	if stderr != "" {
		t.Fatalf("expected no output when nothing was skipped, got %q", stderr)
	}
}

func TestFilterScraperPreservesPartialResults(t *testing.T) {
	partialErr := errors.New("page 2 failed")
	inner := &mockScraper{
		items: []model.MediaItem{
			{URL: "https://cdn.example.com/a.mp4", Title: "a.mp4"},
			{URL: "https://cdn.example.com/b.exe", Title: "b.exe"},
		},
		err: partialErr,
	}
	fs := &filterScraper{inner: inner, mediaOnly: true}

	items, err := fs.Parse(context.Background(), "https://example.com/a/test")
	if !errors.Is(err, partialErr) {
		t.Fatalf("expected the partial error to survive filtering, got %v", err)
	}
	if len(items) != 1 || items[0].Title != "a.mp4" {
		t.Fatalf("expected the parsed a.mp4 to survive alongside the error, got %+v", items)
	}
}

func TestDryRunKeepsPartialMediaItems(t *testing.T) {
	inner := &mockScraper{
		items: []model.MediaItem{
			{URL: "https://cdn.example.com/a.mp4", Title: "a.mp4"},
			{URL: "https://cdn.example.com/b.exe", Title: "b.exe"},
		},
		err: errors.New("page 2 failed"),
	}
	wrapped := applyDryRunFilters(inner, &config.Config{MediaOnly: true})

	var runErr error
	var stderr string
	out := captureStdout(func() {
		stderr = captureStderr(func() {
			runErr = runDryRunWithDiskCheck(wrapped, "https://example.com/a", t.TempDir(), false)
		})
	})
	if runErr != nil {
		t.Fatalf("a partial scrape must warn, not fail the dry-run: %v", runErr)
	}
	if !strings.Contains(stderr, "page 2 failed") {
		t.Fatalf("expected the partial error as a warning, got %q", stderr)
	}
	if !strings.Contains(out, "a.mp4") {
		t.Fatalf("expected the parsed media item in dry-run output, got %q", out)
	}
	if strings.Contains(out, "b.exe") {
		t.Fatalf("expected the skipped non-media item to stay out of dry-run output, got %q", out)
	}
}

func TestFilterScraperMediaOnlySkipsYouTube(t *testing.T) {
	items := []model.MediaItem{{URL: "https://www.youtube.com/watch?v=x", Title: "a video"}}
	yt := newFilterScraper(&mockScraper{name: "youtube", items: items}, &config.Config{MediaOnly: true}, 0, false)
	if got, _ := yt.Parse(context.Background(), "u"); len(got) != 1 {
		t.Fatalf("YouTube must bypass the media filter, got %+v", got)
	}
	file := newFilterScraper(&mockScraper{name: "bunker", items: items}, &config.Config{MediaOnly: true}, 0, false)
	if got, _ := file.Parse(context.Background(), "u"); len(got) != 0 {
		t.Fatalf("file hosts must apply the media filter, got %+v", got)
	}
}

func TestDiskWarningIncludesUnknownItems(t *testing.T) {
	got := diskWarning(3*1024*1024, 1024*1024, 2)
	want := "disk: 1.0M free, 3.0M needed (2.0M short) (2 items with unknown size)"
	if got != want {
		t.Fatalf("diskWarning() = %q, want %q", got, want)
	}
}

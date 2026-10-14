package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

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

func TestRunDryRunEmptyScrapeIsInformational(t *testing.T) {
	// Dry-run is informational: a URL that scrapes zero items (wrapped
	// by the filter scraper under the default media-only config) must
	// print the empty message and exit 0, not fail the run.
	cfg, err := config.Load(emptyTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	scraper := app.PreviewFilters(&fakeDryRunScraper{}, cfg, cliHooks)

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
		}, []app.Entry{{URL: "https://cyberdrop.cr/a/album"}})
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

func TestDryRunKeepsPartialMediaItems(t *testing.T) {
	inner := &mockScraper{
		items: []model.MediaItem{
			{URL: "https://cdn.example.com/a.mp4", Title: "a.mp4"},
			{URL: "https://cdn.example.com/b.exe", Title: "b.exe"},
		},
		err: errors.New("page 2 failed"),
	}
	wrapped := app.PreviewFilters(inner, &config.Config{MediaOnly: true}, cliHooks)

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

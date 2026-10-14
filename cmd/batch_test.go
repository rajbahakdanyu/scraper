package cmd

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestRunBatchPrintURLsRejectsJSON(t *testing.T) {
	restoreBatchFactories(t)
	err := runBatch(
		globalOptions{configPath: emptyTestConfig(t), printURLs: true, json: true},
		[]app.Entry{{URL: "https://cyberdrop.cr/a/one"}},
	)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutual-exclusion error, got %v", err)
	}
}

func TestCollectBatchURLsFromArgs(t *testing.T) {
	urls, err := collectBatchURLs([]string{"https://youtube.com/watch?v=abc", "https://cyberdrop.cr/a/xyz"}, globalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs, got %d: %v", len(urls), urls)
	}
}

func TestCollectBatchURLsFromInputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	content := "# comment\nhttps://youtube.com/watch?v=abc\n\nhttps://cyberdrop.cr/a/xyz\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	urls, err := collectBatchURLs(nil, globalOptions{inputFile: path, inputFileSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs (blanks and comments skipped), got %d: %v", len(urls), urls)
	}
}

func TestCollectBatchURLsTrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	content := "  https://youtube.com/watch?v=abc  \n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	urls, err := collectBatchURLs(nil, globalOptions{inputFile: path, inputFileSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0].URL != "https://youtube.com/watch?v=abc" {
		t.Fatalf("expected trimmed URL, got %v", urls)
	}
}

func TestReadURLsFromFileNotFound(t *testing.T) {
	_, err := app.ReadURLFile(filepath.Join(t.TempDir(), "missing.txt"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadURLsFromReaderParsesItemAlbumPairs(t *testing.T) {
	input := "# comment\n\n" +
		"https://bunkr.cr/a/solo\n" +
		"https://bunkr.cr/f/item1\thttps://bunkr.cr/a/album1\n" +
		"https://bunkr.cr/f/item2\thttps://bunkr.cr/a/album1\textra-ignored\n"
	entries, err := app.ParseURLList(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3: %+v", len(entries), entries)
	}
	if entries[0].URL != "https://bunkr.cr/a/solo" || entries[0].AlbumURL != "" {
		t.Fatalf("single-column entry = %+v", entries[0])
	}
	if entries[1].URL != "https://bunkr.cr/f/item1" || entries[1].AlbumURL != "https://bunkr.cr/a/album1" {
		t.Fatalf("paired entry = %+v", entries[1])
	}
	if entries[2].AlbumURL != "https://bunkr.cr/a/album1" {
		t.Fatalf("extra columns must be ignored, got %+v", entries[2])
	}
}

func TestBatchDestSlugPrefersAlbumURL(t *testing.T) {
	paired := app.Entry{URL: "https://bunkr.cr/f/item1", AlbumURL: "https://bunkr.cr/a/rBSKhzaL"}
	if got := app.DestSlug("bunker", paired); got != "rBSKhzaL" {
		t.Fatalf("app.DestSlug(paired) = %q, want rBSKhzaL", got)
	}
	single := app.Entry{URL: "https://bunkr.cr/a/7OqKQz9N"}
	if got := app.DestSlug("bunker", single); got != "7OqKQz9N" {
		t.Fatalf("app.DestSlug(single) = %q, want 7OqKQz9N", got)
	}
}

func TestRunBatchRunsMixedSitesSequentially(t *testing.T) {
	restoreBatchFactories(t)
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{
			"youtube":   &fakeDryRunScraper{},
			"cyberdrop": &fakeDryRunScraper{},
		}, nil
	}

	var genericCalls, youtubeCalls int
	batchYouTubeScraper = func() model.Scraper { return &fakeDryRunScraper{} }
	batchYouTubeDownloader = func(model.Scraper, *config.Config) (model.Downloader, error) {
		youtubeCalls++
		return newFakePlainDownloader(), nil
	}
	batchGenericDownloader = func(*config.Config) (model.Downloader, error) {
		genericCalls++
		return newFakePlainDownloader(), nil
	}

	err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true}, []app.Entry{
		{URL: "https://youtube.com/watch?v=abc"},
		{URL: "https://cyberdrop.cr/a/album"},
	})
	// The fake scrapers return zero items, so the run ends in the
	// no-items partial error (Task: empty scrapes exit non-zero); the
	// assertion that matters here is that BOTH groups ran.
	if err != nil && !strings.Contains(err.Error(), "no media items found") {
		t.Fatalf("mixed batch failed unexpectedly: %v", err)
	}
	if genericCalls != 1 || youtubeCalls != 1 {
		t.Fatalf("expected one run per group, got generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
}

func TestRunBatchPlainTreatsEmptyScrapeAsFailure(t *testing.T) {
	// An album that scrapes zero items (dead URL, DOM change) must fail
	// the batch instead of looking successful.
	restoreBatchFactories(t)
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": &fakeDryRunScraper{}}, nil
	}

	err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true}, []app.Entry{
		{URL: "https://cyberdrop.cr/a/empty"},
	})
	if err == nil || !strings.Contains(err.Error(), "no media items found") {
		t.Fatalf("an empty scrape must fail the batch, got %v", err)
	}
}

func TestRunBatchContinuesAfterGroupFailure(t *testing.T) {
	restoreBatchFactories(t)
	var genericCalls int
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": &fakeDryRunScraper{}}, nil
	}
	batchYouTubeScraper = func() model.Scraper { return &fakeDryRunScraper{} }
	batchYouTubeDownloader = func(model.Scraper, *config.Config) (model.Downloader, error) {
		return nil, errors.New("youtube downloader exploded")
	}
	batchGenericDownloader = func(*config.Config) (model.Downloader, error) {
		genericCalls++
		return newFakePlainDownloader(), nil
	}

	err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true}, []app.Entry{
		{URL: "https://youtube.com/watch?v=abc"},
		{URL: "https://cyberdrop.cr/a/album"},
	})
	if err == nil {
		t.Fatal("the youtube group failure must surface")
	}
	if genericCalls != 1 {
		t.Fatalf("generic group must still run after a youtube group failure, ran %d times", genericCalls)
	}
}

func TestRunBatchDryRunJSONLoopsOverURLs(t *testing.T) {
	restoreBatchFactories(t)
	fake := &fakeDryRunScraper{items: []model.MediaItem{{URL: "https://example.com/item.mp4", Title: "unique-fixture-title.mp4"}}}
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": fake}, nil
	}

	var err error
	out := captureStdout(func() {
		err = runBatch(globalOptions{
			configPath: emptyTestConfig(t),
			dryRun:     true,
			json:       true,
		}, []app.Entry{
			{URL: "https://cyberdrop.cr/a/one"},
			{URL: "https://cyberdrop.cr/a/two"},
		})
	})
	if err != nil {
		t.Fatalf("expected dry-run batch to succeed, got %v", err)
	}
	if strings.Count(out, `"dryRun"`) != 2 {
		t.Fatalf("expected one JSON dry-run document per URL, got %d:\n%s", strings.Count(out, `"dryRun"`), out)
	}
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") || !strings.Contains(out, "unique-fixture-title.mp4") {
		t.Fatalf("expected both URLs and the item title, got %q", out)
	}
}

func TestRunBatchSelectsDownloaderBySiteAndWiresDestinations(t *testing.T) {
	restoreBatchFactories(t)
	genericScraper := &fakeDryRunScraper{items: []model.MediaItem{{URL: "https://cyberdrop.cr/item", Title: "generic.mp4"}}}
	youtubeScraper := &fakeDryRunScraper{name: "youtube", items: []model.MediaItem{{URL: "https://youtube.com/item", Title: "youtube"}}}
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"cyberdrop": genericScraper}, nil
	}

	var genericCalls, youtubeCalls int
	var genericDL, youtubeDL *fakePlainDownloader
	batchGenericDownloader = func(*config.Config) (model.Downloader, error) {
		genericCalls++
		genericDL = newFakePlainDownloader()
		return genericDL, nil
	}
	batchYouTubeScraper = func() model.Scraper { return youtubeScraper }
	batchYouTubeDownloader = func(model.Scraper, *config.Config) (model.Downloader, error) {
		youtubeCalls++
		youtubeDL = newFakePlainDownloader()
		return youtubeDL, nil
	}

	base := t.TempDir()
	if err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true, output: base, outputSet: true}, []app.Entry{{URL: "https://cyberdrop.cr/a/album"}}); err != nil {
		t.Fatalf("generic batch failed: %v", err)
	}
	if genericCalls != 1 || youtubeCalls != 0 {
		t.Fatalf("generic batch selected wrong downloader: generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
	if len(genericDL.dests) != 1 || !strings.HasSuffix(genericDL.dests[0], filepath.Join(base, "album")) {
		t.Fatalf("generic batch destination = %v, want album slug under output", genericDL.dests)
	}

	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"youtube": youtubeScraper}, nil
	}
	if err := runBatch(globalOptions{configPath: emptyTestConfig(t), noTui: true, output: base, outputSet: true}, []app.Entry{{URL: "https://youtube.com/watch?v=abc"}}); err != nil {
		t.Fatalf("youtube batch failed: %v", err)
	}
	if youtubeCalls != 1 || genericCalls != 1 {
		t.Fatalf("youtube batch selected wrong downloader: generic=%d youtube=%d", genericCalls, youtubeCalls)
	}
	if len(youtubeDL.dests) != 1 || !strings.HasSuffix(youtubeDL.dests[0], filepath.Join("abc")) {
		t.Fatalf("youtube batch destination = %v, want video-ID slug under output", youtubeDL.dests)
	}
}

func TestBatchDryRunPrintsPerURLErrors(t *testing.T) {
	restoreBatchFactories(t)
	bad := &fakeDryRunScraper{name: "bunker", err: errors.New("album page returned no file links")}
	batchScraperRegistry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		return map[string]model.Scraper{"bunker": bad}, nil
	}

	var runErr error
	stderr := captureStderr(func() {
		runErr = runBatch(globalOptions{configPath: emptyTestConfig(t), dryRun: true, noTui: true}, []app.Entry{{URL: "https://bunkr.cr/a/one"}})
	})
	if runErr == nil {
		t.Fatal("expected the batch dry-run to report the failure")
	}
	if !strings.Contains(stderr, "https://bunkr.cr/a/one") || !strings.Contains(stderr, "no file links") {
		t.Fatalf("expected the failing URL and its error on stderr, got %q", stderr)
	}
}

func TestHelpRequestedOnlyWithoutAnyURLSource(t *testing.T) {
	if !helpRequested(nil, false, true) {
		t.Fatal("no args, no input file, terminal stdin: expected help")
	}
	if helpRequested(nil, true, true) {
		t.Fatal("--input-file set: must run, not print help")
	}
	if helpRequested(nil, false, false) {
		t.Fatal("piped stdin: must run, not print help")
	}
	if helpRequested([]string{"https://example.com/x"}, false, true) {
		t.Fatal("positional args: must run, not print help")
	}
}

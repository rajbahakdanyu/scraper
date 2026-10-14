package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

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
	err := runPlain(dl, itemsScraper(items, nil), root, "album", "", &bytes.Buffer{})
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
	err := runPlain(dl, itemsScraper(items, nil), dest, "album", "", &buf)
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
	if err := runPlain(dl, itemsScraper(items, nil), dest, "album", "", &buf); err != nil {
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
		{URL: "https://example.com/bad.mp4", Title: "bad.mp4", PageURL: "https://example.com/f/bad"},
	}
	dl.errs["https://example.com/bad.mp4"] = fmt.Errorf("boom")
	dl.scripted["https://example.com/good.mp4"] = []model.ProgressEvent{
		{Status: model.StatusCompleted, Filename: "good.mp4"},
	}

	var buf bytes.Buffer
	err := runPlain(dl, itemsScraper(items, nil), dest, "album", "https://example.com/a/album", &buf)
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
	matches, _ := filepath.Glob(filepath.Join(dest, "scraper-failed-*.txt"))
	if len(matches) != 1 {
		t.Fatalf("retry files = %v, want exactly one", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://example.com/f/bad\thttps://example.com/a/album") {
		t.Errorf("retry list missing the item/album pair:\n%s", data)
	}
	if !strings.Contains(out, "Retry list:") {
		t.Errorf("expected the summary to name the retry list, got: %q", out)
	}
}

func TestRunPlainTreatsCancelledItemsAsInterrupted(t *testing.T) {
	dl := newFakePlainDownloader()
	item := model.MediaItem{URL: "https://example.com/cancelled.mp4", Title: "cancelled.mp4"}
	dl.errs[item.URL] = &model.CancelledError{URL: item.URL}

	var buf bytes.Buffer
	if err := runPlain(dl, itemsScraper([]model.MediaItem{item}, nil), t.TempDir(), "", "", &buf); err != nil {
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

func TestRunPlainPropagatesScrapeError(t *testing.T) {
	dl := newFakePlainDownloader()
	wantErr := fmt.Errorf("scrape boom")

	var buf bytes.Buffer
	err := runPlain(dl, itemsScraper(nil, wantErr), t.TempDir(), "album", "", &buf)
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
	err := runPlain(dl, itemsScraper(nil, nil), t.TempDir(), "album", "", &buf)
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
	if err := runPlain(dl, itemsScraper(items, nil), dest, "album", "", &buf); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(dl.downloaded) != 1 {
		t.Errorf("expected the item to be downloaded, got %d downloads", len(dl.downloaded))
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
	if err := runPlain(dl, itemsScraper(items, nil), dest, "album", "", &buf); err != nil {
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

package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

func TestFailureReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"retryable status includes host", model.NewHTTPStatusError(errors.New("HTTP 503"), 503, "https://pc9.scdn.st/storage/x.mp4", 0), "HTTP 503 from pc9.scdn.st"},
		{"not found", model.NewNonRetryableStatus(errors.New("HTTP 404"), 404), "HTTP 404 (not found)"},
		{"connection dropped", io.ErrUnexpectedEOF, "connection dropped"},
		{"timeout", os.ErrDeadlineExceeded, "timed out"},
		{"other", errors.New("boom"), "other error"},
		{"nil", nil, "unknown error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := failureReason(tt.err); got != tt.want {
				t.Fatalf("failureReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRetryReportWritesListAndSummary(t *testing.T) {
	dir := t.TempDir()
	failures := []failedItem{
		{
			Item: model.MediaItem{URL: "https://dl.bunkr.cr/file/1", PageURL: "https://bunkr.cr/f/aaa", Title: "a"},
			Err:  model.NewHTTPStatusError(errors.New("HTTP 503"), 503, "https://pc9.scdn.st/storage/x.mp4", 0),
		},
		{
			Item: model.MediaItem{URL: "https://dl.bunkr.cr/file/2", PageURL: "https://bunkr.cr/f/bbb", Title: "b"},
			Err:  io.ErrUnexpectedEOF,
		},
	}

	path, summary, err := retryReport(dir, failures, time.Date(2026, 9, 26, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("retryReport() error = %v", err)
	}
	if path == "" || !strings.HasPrefix(filepath.Base(path), "scraper-failed-") {
		t.Fatalf("path = %q, want a scraper-failed-*.txt file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"https://bunkr.cr/f/aaa", "https://bunkr.cr/f/bbb", "# scraper retry list"} {
		if !strings.Contains(content, want) {
			t.Errorf("retry list missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "dl.bunkr.cr") {
		t.Errorf("retry list should prefer page URLs:\n%s", content)
	}
	for _, want := range []string{"HTTP 503 from pc9.scdn.st", "connection dropped", path} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestRetryReportDeduplicatesAndSkipsEmptyURLs(t *testing.T) {
	dir := t.TempDir()
	failures := []failedItem{
		{Item: model.MediaItem{URL: "https://bunkr.cr/f/same"}, Err: errors.New("boom")},
		{Item: model.MediaItem{URL: "https://bunkr.cr/f/same"}, Err: errors.New("boom")},
		{Item: model.MediaItem{Title: "no-url"}, Err: errors.New("boom")},
	}

	path, _, err := retryReport(dir, failures, time.Now())
	if err != nil {
		t.Fatalf("retryReport() error = %v", err)
	}
	data, _ := os.ReadFile(path)
	if got := strings.Count(string(data), "https://bunkr.cr/f/same"); got != 1 {
		t.Fatalf("duplicate URL written %d times, want 1", got)
	}
}

func TestRetryReportWritesNothingWithoutFailures(t *testing.T) {
	dir := t.TempDir()
	path, summary, err := retryReport(dir, nil, time.Now())
	if err != nil || path != "" || summary != "" {
		t.Fatalf("retryReport() = (%q, %q, %v), want empty result", path, summary, err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "scraper-failed-*.txt"))
	if len(matches) != 0 {
		t.Fatalf("unexpected retry files: %v", matches)
	}
}

func TestCollectFailuresSkipsCancelledAndNil(t *testing.T) {
	items := []model.MediaItem{
		{URL: "https://x/1"},
		{URL: "https://x/2"},
		{URL: "https://x/3"},
	}
	errs := []error{nil, &model.CancelledError{URL: "https://x/2"}, errors.New("boom")}
	got := collectFailures(items, errs)
	if len(got) != 1 || got[0].Item.URL != "https://x/3" {
		t.Fatalf("collectFailures() = %+v, want only the failed item", got)
	}
}

func TestFailuresFromDashboard(t *testing.T) {
	in := []dashboard.FailedItem{{Item: model.MediaItem{URL: "https://x/1"}, Err: errors.New("boom")}}
	out := failuresFromDashboard(in)
	if len(out) != 1 || out[0].Item.URL != "https://x/1" || out[0].Err == nil {
		t.Fatalf("failuresFromDashboard() = %+v", out)
	}
}

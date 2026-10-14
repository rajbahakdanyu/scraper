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
	failures := []model.FailedItem{
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
	failures := []model.FailedItem{
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
	got := collectFailures(items, errs, "https://bunkr.cr/a/rBSKhzaL")
	if len(got) != 1 || got[0].Item.URL != "https://x/3" || got[0].AlbumURL != "https://bunkr.cr/a/rBSKhzaL" {
		t.Fatalf("collectFailures() = %+v, want the failed item with its album URL", got)
	}
}

func TestRetryReportUsesAlbumURLWhenItemHasNoPage(t *testing.T) {
	dir := t.TempDir()
	failures := []model.FailedItem{
		{Item: model.MediaItem{URL: "https://simp6.cuckcapital.cr/images4/a.jpg"}, Err: errors.New("boom"), AlbumURL: "https://goonbox.cr/a/aZsRlh"},
		{Item: model.MediaItem{URL: "https://simp6.cuckcapital.cr/images4/b.jpg"}, Err: errors.New("boom"), AlbumURL: "https://goonbox.cr/a/aZsRlh"},
	}

	path, summary, err := retryReport(dir, failures, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "cuckcapital") {
		t.Fatalf("retry list should not contain unscrapable CDN URLs:\n%s", content)
	}
	if got := strings.Count(content, "https://goonbox.cr/a/aZsRlh"); got != 1 {
		t.Fatalf("album line written %d times, want 1:\n%s", got, content)
	}
	if !strings.Contains(summary, "Failed 2 item(s)") {
		t.Fatalf("summary = %q, want the failure count", summary)
	}
}

func TestRetryReportRedactsAlbumURLQuery(t *testing.T) {
	dir := t.TempDir()
	failures := []model.FailedItem{
		{Item: model.MediaItem{PageURL: "https://bunkr.cr/f/item1"}, Err: errors.New("boom"), AlbumURL: "https://bunkr.cr/a/rBSKhzaL?token=secret"},
	}

	path, _, err := retryReport(dir, failures, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "secret") {
		t.Fatalf("retry list leaked an album URL token:\n%s", content)
	}
	if !strings.Contains(content, "https://bunkr.cr/f/item1\thttps://bunkr.cr/a/rBSKhzaL") {
		t.Fatalf("expected a redacted album column:\n%s", content)
	}
}

func TestRetryReportRedactsPageURLQuery(t *testing.T) {
	dir := t.TempDir()
	failures := []model.FailedItem{
		{Item: model.MediaItem{
			URL:     "https://cdn.example.com/files/v.mp4?token=SECRET",
			PageURL: "https://bunkr.cr/f/abc?token=SECRET",
		}, Err: errors.New("boom"), AlbumURL: "https://bunkr.cr/a/rBSKhzaL"},
	}

	path, _, err := retryReport(dir, failures, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "token=SECRET") {
		t.Fatalf("PageURL query must be redacted, got:\n%s", content)
	}
	if !strings.Contains(content, "https://bunkr.cr/f/abc\thttps://bunkr.cr/a/rBSKhzaL") {
		t.Fatalf("redacted PageURL must still pair with its album, got:\n%s", content)
	}
}

func TestRetryReportWritesItemAlbumPairs(t *testing.T) {
	dir := t.TempDir()
	failures := []model.FailedItem{
		{Item: model.MediaItem{PageURL: "https://bunkr.cr/f/item1"}, Err: errors.New("boom"), AlbumURL: "https://bunkr.cr/a/rBSKhzaL"},
		{Item: model.MediaItem{PageURL: "https://bunkr.cr/f/item2"}, Err: errors.New("boom"), AlbumURL: "https://bunkr.cr/a/rBSKhzaL"},
		{Item: model.MediaItem{PageURL: "https://bunkr.cr/f/item3"}, Err: errors.New("boom")},
	}

	path, _, err := retryReport(dir, failures, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "https://bunkr.cr/f/item1\thttps://bunkr.cr/a/rBSKhzaL") {
		t.Fatalf("expected an item/album pair line:\n%s", content)
	}
	if !strings.Contains(content, "\nhttps://bunkr.cr/f/item3\n") {
		t.Fatalf("expected a single-column line for an item without an album:\n%s", content)
	}
	if strings.Contains(content, "item3\t") {
		t.Fatalf("item without an album must not get a tab:\n%s", content)
	}
}

func TestRetryReportRedactsSignedFallbackURL(t *testing.T) {
	dir := t.TempDir()
	failures := []model.FailedItem{
		{Item: model.MediaItem{URL: "https://cdn.example/file.mp4?token=secret&ex=123", Title: "signed"}, Err: errors.New("boom")},
	}

	path, summary, err := retryReport(dir, failures, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "secret") {
		t.Fatalf("retry list leaked a signed URL:\n%s", data)
	}
	if strings.Contains(summary, "secret") {
		t.Fatalf("summary leaked a signed URL:\n%s", summary)
	}
	if !strings.Contains(string(data), "https://cdn.example/file.mp4") {
		t.Fatalf("retry list lost the URL path:\n%s", data)
	}
}

func TestRetryReportErrorsOnUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to read-only directories")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	failures := []model.FailedItem{{Item: model.MediaItem{URL: "https://bunkr.cr/f/x"}, Err: errors.New("boom")}}
	path, summary, err := retryReport(dir, failures, time.Now())
	if err == nil {
		t.Fatalf("expected an error for an unwritable directory, got path=%q", path)
	}
	if path != "" || summary != "" {
		t.Fatalf("retryReport() = (%q, %q), want empty results on error", path, summary)
	}
}

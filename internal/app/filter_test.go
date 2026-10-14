package app

import (
	"context"
	"errors"
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
	kept := ApplyMediaFilter(items, []string{"cbz"}, Hooks{})
	if len(kept) != 1 || kept[0].Title != "comic.cbz" {
		t.Fatalf("ApplyMediaFilter() = %+v, want only the comic", kept)
	}
}

func TestFilterScraperScrapeDelay(t *testing.T) {
	prev := ScrapeDelaySleeper
	sleeper := &recordingDelaySleeper{}
	ScrapeDelaySleeper = sleeper
	t.Cleanup(func() { ScrapeDelaySleeper = prev })

	fs := &FilterScraper{
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
	stderr := captureNotices(func(h Hooks) {
		capped = ApplyAlbumItemCap(items, 2, h)
	})
	if len(capped) != 2 || capped[0].Title != "a.mp4" || capped[1].Title != "b.mp4" {
		t.Fatalf("applyAlbumItemCap() = %+v, want the first two items", capped)
	}
	if !strings.Contains(stderr, "keeping first 2 of 3") {
		t.Fatalf("expected a cap warning, got %q", stderr)
	}

	if got := ApplyAlbumItemCap(items, 0, Hooks{}); len(got) != 3 {
		t.Fatalf("cap 0 must be unlimited, got %d items", len(got))
	}
	if got := ApplyAlbumItemCap(items, 5, Hooks{}); len(got) != 3 {
		t.Fatalf("cap above length must keep all items, got %d", len(got))
	}
}

func TestDiskCheckWarnsForMissingOutputDir(t *testing.T) {
	msg := DiskCheck([]model.MediaItem{{Title: "big.mp4", Size: 1 << 62}}, filepath.Join(t.TempDir(), "missing"))
	if !strings.Contains(msg, "short") {
		t.Fatalf("a huge download into a fresh directory must warn, got %q", msg)
	}
}

func TestFilterScraperParse(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{
		{URL: "https://cdn.example.com/video.mp4", Size: 1024},
		{URL: "https://cdn.example.com/image.jpg", Size: 512},
	}}
	fs := &FilterScraper{
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
	fs := &FilterScraper{
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
	fs := &FilterScraper{inner: inner, mediaOnly: true}

	var items []model.MediaItem
	stderr := captureNotices(func(h Hooks) {
		fs.hooks = h
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
	fs := &FilterScraper{inner: inner, mediaOnly: true}

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
	fs := &FilterScraper{inner: inner, mediaOnly: true, include: []string{"*.zip"}}

	items, _ := fs.Parse(context.Background(), "https://example.com/a/test")
	if len(items) != 1 || items[0].Title != "b.zip" {
		t.Fatalf("expected only b.zip after media + include filters, got %+v", items)
	}
}

func TestApplyDryRunFiltersWrapsWhenOnlyMediaOnly(t *testing.T) {
	inner := &mockScraper{items: []model.MediaItem{{Title: "a.mp4"}}}
	cfg := &config.Config{MediaOnly: true}

	wrapped := PreviewFilters(inner, cfg, Hooks{})
	if _, ok := wrapped.(*FilterScraper); !ok {
		t.Fatalf("expected a FilterScraper wrapper, got %T", wrapped)
	}
}

func TestApplyMediaFilterAllSkipped(t *testing.T) {
	var kept []model.MediaItem
	stderr := captureNotices(func(h Hooks) {
		kept = ApplyMediaFilter([]model.MediaItem{{Title: "setup.exe"}}, nil, h)
	})
	if len(kept) != 0 {
		t.Fatalf("expected no items, got %+v", kept)
	}
	if !strings.Contains(stderr, "1 skipped non-media") || !strings.Contains(stderr, "No media items found.") {
		t.Fatalf("expected skip summary and empty notice, got %q", stderr)
	}
}

func TestApplyMediaFilterEmptyInputIsSilent(t *testing.T) {
	stderr := captureNotices(func(h Hooks) {
		kept := ApplyMediaFilter(nil, nil, h)
		if len(kept) != 0 {
			t.Fatalf("expected no items, got %+v", kept)
		}
	})
	if strings.Contains(stderr, "No media items found.") {
		t.Fatalf("empty input must not print the media notice, got %q", stderr)
	}
}

func TestApplyMediaFilterSilentWhenNothingSkipped(t *testing.T) {
	stderr := captureNotices(func(h Hooks) {
		ApplyMediaFilter([]model.MediaItem{{Title: "a.mp4"}}, nil, h)
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
	fs := &FilterScraper{inner: inner, mediaOnly: true}

	items, err := fs.Parse(context.Background(), "https://example.com/a/test")
	if !errors.Is(err, partialErr) {
		t.Fatalf("expected the partial error to survive filtering, got %v", err)
	}
	if len(items) != 1 || items[0].Title != "a.mp4" {
		t.Fatalf("expected the parsed a.mp4 to survive alongside the error, got %+v", items)
	}
}

func TestFilterScraperMediaOnlySkipsYouTube(t *testing.T) {
	items := []model.MediaItem{{URL: "https://www.youtube.com/watch?v=x", Title: "a video"}}
	yt := NewFilterScraper(&mockScraper{name: "youtube", items: items}, &config.Config{MediaOnly: true}, 0, false, Hooks{})
	if got, _ := yt.Parse(context.Background(), "u"); len(got) != 1 {
		t.Fatalf("YouTube must bypass the media filter, got %+v", got)
	}
	file := NewFilterScraper(&mockScraper{name: "bunker", items: items}, &config.Config{MediaOnly: true}, 0, false, Hooks{})
	if got, _ := file.Parse(context.Background(), "u"); len(got) != 0 {
		t.Fatalf("file hosts must apply the media filter, got %+v", got)
	}
}

func TestDiskWarningIncludesUnknownItems(t *testing.T) {
	got := DiskWarning(3*1024*1024, 1024*1024, 2)
	want := "disk: 1.0M free, 3.0M needed (2.0M short) (2 items with unknown size)"
	if got != want {
		t.Fatalf("DiskWarning() = %q, want %q", got, want)
	}
}

// captureNotices runs f with hooks that record every notice, one per line.
func captureNotices(f func(h Hooks)) string {
	var b strings.Builder
	f(Hooks{Notice: func(msg string) { b.WriteString(msg + "\n") }})
	return b.String()
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

func (m *mockScraper) Parse(context.Context, string) ([]model.MediaItem, error) {
	return m.items, m.err
}

type recordingDelaySleeper struct{ delays []time.Duration }

func (s *recordingDelaySleeper) Sleep(time.Duration) {}

func (s *recordingDelaySleeper) SleepCtx(_ context.Context, d time.Duration) error {
	s.delays = append(s.delays, d)
	return nil
}

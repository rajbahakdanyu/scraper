package model

import (
	"testing"
)

func TestFilterItemsIncludeGlob(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/video.mp4", Size: 1024},
		{URL: "https://cdn.example.com/image.jpg", Size: 512},
		{URL: "https://cdn.example.com/audio.m4a", Size: 256},
	}
	filtered, s := FilterItems(items, []string{"*.mp4", "*.m4a"}, nil, 0, 0)
	if s.Total != 3 || s.Kept != 2 || s.ExcludedByGlob != 1 || s.ExcludedBySize != 0 {
		t.Errorf("summary: %+v", s)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 items, got %d", len(filtered))
	}
}

func TestFilterItemsExcludeGlob(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/video.mp4"},
		{URL: "https://cdn.example.com/image.jpg"},
		{URL: "https://cdn.example.com/thumb.webp"},
	}
	filtered, s := FilterItems(items, nil, []string{"*.webp", "*.jpg"}, 0, 0)
	if s.Kept != 1 {
		t.Errorf("expected 1 kept, got %d", s.Kept)
	}
	if len(filtered) != 1 || filtered[0].URL != "https://cdn.example.com/video.mp4" {
		t.Errorf("unexpected filtered: %+v", filtered)
	}
}

func TestFilterItemsIncludeAndExclude(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/video.mp4"},
		{URL: "https://cdn.example.com/video.webm"},
		{URL: "https://cdn.example.com/image.jpg"},
	}
	filtered, s := FilterItems(items, []string{"video*"}, []string{"*.webm"}, 0, 0)
	if s.Kept != 1 {
		t.Errorf("expected 1 kept, got %d", s.Kept)
	}
	if len(filtered) != 1 || filtered[0].URL != "https://cdn.example.com/video.mp4" {
		t.Errorf("unexpected: %+v", filtered)
	}
}

func TestFilterItemsMinSize(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/small.jpg", Size: 100},
		{URL: "https://cdn.example.com/large.mp4", Size: 10000},
		{URL: "https://cdn.example.com/unknown.bin", Size: 0},
	}
	filtered, s := FilterItems(items, nil, nil, 1000, 0)
	if s.Kept != 2 || s.ExcludedBySize != 1 {
		t.Errorf("summary: %+v", s)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 items (unknown size kept), got %d", len(filtered))
	}
}

func TestFilterItemsMaxSize(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/small.jpg", Size: 100},
		{URL: "https://cdn.example.com/large.mp4", Size: 10000},
	}
	filtered, s := FilterItems(items, nil, nil, 0, 500)
	if s.Kept != 1 {
		t.Errorf("expected 1 kept, got %d", s.Kept)
	}
	if len(filtered) != 1 || filtered[0].URL != "https://cdn.example.com/small.jpg" {
		t.Errorf("unexpected: %+v", filtered)
	}
}

func TestFilterItemsEmptyInput(t *testing.T) {
	filtered, s := FilterItems(nil, []string{"*.mp4"}, nil, 0, 0)
	if s.Total != 0 || s.Kept != 0 {
		t.Errorf("summary: %+v", s)
	}
	if len(filtered) != 0 {
		t.Errorf("expected empty result, got %d", len(filtered))
	}
}

func TestFilterItemsNoFilters(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/video.mp4"},
		{URL: "https://cdn.example.com/image.jpg"},
	}
	filtered, s := FilterItems(items, nil, nil, 0, 0)
	if s.Kept != 2 || s.ExcludedByGlob != 0 || s.ExcludedBySize != 0 {
		t.Errorf("summary: %+v", s)
	}
	if len(filtered) != 2 {
		t.Errorf("expected all items kept, got %d", len(filtered))
	}
}

func TestFilterItemsURLWithQueryString(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/video.mp4?token=abc&expires=123"},
	}
	filtered, s := FilterItems(items, []string{"*.mp4"}, nil, 0, 0)
	if s.Kept != 1 {
		t.Errorf("expected 1 kept (query stripped), got %d", s.Kept)
	}
	if len(filtered) != 1 {
		t.Fatal("expected video to match glob after query strip")
	}
}

func TestFilterItemsGlobNoMatch(t *testing.T) {
	items := []MediaItem{
		{URL: "https://cdn.example.com/video.mp4"},
	}
	filtered, s := FilterItems(items, []string{"*.jpg"}, nil, 0, 0)
	if s.Kept != 0 || s.ExcludedByGlob != 1 {
		t.Errorf("summary: %+v", s)
	}
	if len(filtered) != 0 {
		t.Errorf("expected 0 items, got %d", len(filtered))
	}
}

func TestFilterSummaryString(t *testing.T) {
	s := FilterSummary{Total: 10, Kept: 7, ExcludedByGlob: 2, ExcludedBySize: 1}
	str := s.String()
	if str == "" {
		t.Error("expected non-empty string")
	}
}

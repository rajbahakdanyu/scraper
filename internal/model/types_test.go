package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMediaItemFields(t *testing.T) {
	item := MediaItem{
		URL:      "https://example.com/video.mp4",
		Title:    "Test Video",
		Size:     1024,
		MimeType: "video/mp4",
		Type:     MediaTypeVideo,
	}

	if item.URL != "https://example.com/video.mp4" {
		t.Errorf("unexpected URL: %s", item.URL)
	}
	if item.Title != "Test Video" {
		t.Errorf("unexpected title: %s", item.Title)
	}
	if item.Size != 1024 {
		t.Errorf("unexpected size: %d", item.Size)
	}
	if item.Type != MediaTypeVideo {
		t.Errorf("unexpected type: %s", item.Type)
	}
}

func TestTitleOfFallsBackToURL(t *testing.T) {
	if got := TitleOf(MediaItem{Title: "named", URL: "https://example.com/file"}); got != "named" {
		t.Fatalf("TitleOf() = %q, want named", got)
	}
	if got := TitleOf(MediaItem{URL: "https://example.com/file"}); got != "https://example.com/file" {
		t.Fatalf("TitleOf() fallback = %q, want URL", got)
	}
}

func TestCancelledError(t *testing.T) {
	err := &CancelledError{URL: "https://example.com/video.mp4"}
	if got, want := err.Error(), "download cancelled: https://example.com/video.mp4"; got != want {
		t.Fatalf("CancelledError.Error() = %q, want %q", got, want)
	}
}

func TestIsCancelled(t *testing.T) {
	base := &CancelledError{URL: "https://example.com/video.mp4"}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "CancelledError", err: base, want: true},
		{name: "ordinary error", err: fmt.Errorf("download cancelled: unrelated text"), want: false},
		{name: "wrapped CancelledError", err: fmt.Errorf("download failed: %w", base), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCancelled(tt.err); got != tt.want {
				t.Errorf("IsCancelled(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestPartialErrorFiltersNilErrors(t *testing.T) {
	first := errors.New("first")
	err := NewPartialError("album", []error{nil, first, nil})
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("expected PartialError, got %T", err)
	}
	if len(partial.Errors) != 1 || !errors.Is(partial, first) {
		t.Fatalf("unexpected aggregate: %+v", partial)
	}
	if NewPartialError("album", []error{nil}) != nil {
		t.Fatal("all-nil errors should not produce a PartialError")
	}
}

func TestProgressEventFields(t *testing.T) {
	start := time.Now()
	event := ProgressEvent{
		Bytes:    512,
		Total:    1024,
		Status:   StatusDownloading,
		Filename: "output.mp4",
		Start:    start,
	}

	if event.Bytes != 512 {
		t.Errorf("unexpected bytes: %d", event.Bytes)
	}
	if event.Status != StatusDownloading {
		t.Errorf("unexpected status: %d", event.Status)
	}
}

func TestMediaTypeForName(t *testing.T) {
	tests := []struct {
		name     string
		expected MediaType
	}{
		{"photo.jpg", MediaTypeImage},
		{"photo.JPEG", MediaTypeImage},
		{"pic.PNG", MediaTypeImage},
		{"anim.gif", MediaTypeImage},
		{"art.webp", MediaTypeImage},
		{"clip.mp4", MediaTypeVideo},
		{"movie.mkv", MediaTypeVideo},
		{"unknown.xyz", MediaTypeVideo},
	}

	for _, tt := range tests {
		got := MediaTypeForName(tt.name)
		if got != tt.expected {
			t.Errorf("MediaTypeForName(%s) = %s, want %s", tt.name, got, tt.expected)
		}
	}
}

func TestJSONTypesSerialization(t *testing.T) {
	item := JSONItem{
		Title:    "test.mp4",
		URL:      "https://example.com/test.mp4",
		Size:     1024,
		MimeType: "video/mp4",
		Type:     "video",
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"title":"test.mp4"`) {
		t.Errorf("expected title in JSON, got: %s", string(data))
	}
	if strings.Contains(string(data), `"size":0`) {
		t.Errorf("expected non-zero size in JSON, got: %s", string(data))
	}
}

func TestJSONEventSerialization(t *testing.T) {
	evt := JSONEvent{
		Event:  "item",
		Title:  "test.mp4",
		Status: "completed",
		Album:  "album-1",
	}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"event":"item"`) {
		t.Errorf("expected event field, got: %s", string(data))
	}
}

func TestDownloadStatusString(t *testing.T) {
	tests := []struct {
		status DownloadStatus
		want   string
	}{
		{StatusPending, "pending"},
		{StatusDownloading, "downloading"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusCancelled, "cancelled"},
		{DownloadStatus(999), "unknown"},
	}
	for _, tt := range tests {
		if got := DownloadStatusString(tt.status); got != tt.want {
			t.Errorf("DownloadStatusString(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestItemToJSONItem(t *testing.T) {
	mi := MediaItem{
		Title:    "photo.jpg",
		URL:      "https://example.com/photo.jpg",
		Size:     2048,
		MimeType: "image/jpeg",
		Type:     MediaTypeImage,
	}
	ji := ItemToJSONItem(mi)
	if ji.Title != "photo.jpg" || ji.Size != 2048 || ji.Type != "image" {
		t.Errorf("JSONItem mismatch: %+v", ji)
	}
}

func TestItemToJSONItemFallsBackToURL(t *testing.T) {
	mi := MediaItem{URL: "https://example.com/fallback.mp4", Size: 100}
	ji := ItemToJSONItem(mi)
	if ji.Title != "https://example.com/fallback.mp4" {
		t.Errorf("expected URL as title fallback, got %q", ji.Title)
	}
}

func TestPartialErrorZeroValueDoesNotPanic(t *testing.T) {
	var zero PartialError
	if got := zero.Error(); got != " incomplete" {
		t.Fatalf("zero PartialError.Error() = %q", got)
	}
	op := &PartialError{Operation: "album"}
	if got := op.Error(); got != "album incomplete" {
		t.Fatalf("error = %q, want album incomplete", got)
	}
}

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
		URL:       "https://example.com/video.mp4",
		Title:     "Test Video",
		Size:      1024,
		MimeType:  "video/mp4",
		Type:      MediaTypeVideo,
		Thumbnail: "https://example.com/thumb.jpg",
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

func TestPermanentError(t *testing.T) {
	inner := errors.New("file deleted upstream")
	err := error(&PermanentError{Err: inner})
	if err.Error() != "file deleted upstream" {
		t.Errorf("unexpected message: %v", err)
	}
	if !errors.Is(err, inner) {
		t.Error("PermanentError should unwrap to its inner error")
	}
	var pe *PermanentError
	if !errors.As(fmt.Errorf("resolve signed URL: %w", err), &pe) {
		t.Error("PermanentError should be discoverable through wrapping")
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
		Speed:    256.5,
		Status:   StatusDownloading,
		Filename: "output.mp4",
		Start:    start,
	}

	if event.Bytes != 512 {
		t.Errorf("unexpected bytes: %d", event.Bytes)
	}
	if event.Speed != 256.5 {
		t.Errorf("unexpected speed: %f", event.Speed)
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

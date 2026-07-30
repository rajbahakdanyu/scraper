package model

import (
	"errors"
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

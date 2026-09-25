package model

import "testing"

func TestIsAllowedMediaItem(t *testing.T) {
	tests := []struct {
		name string
		item MediaItem
		want bool
	}{
		{"video", MediaItem{Title: "clip.mp4"}, true},
		{"uppercase", MediaItem{Title: "CLIP.MP4"}, true},
		{"trailing space", MediaItem{Title: "clip.mp4 "}, true},
		{"audio", MediaItem{Title: "song.flac"}, true},
		{"image", MediaItem{Title: "photo.JPEG"}, true},
		{"multi-part archive", MediaItem{Title: "set.tar.gz"}, true},
		{"7z archive", MediaItem{Title: "set.7z"}, true},
		{"title without extension, URL has one", MediaItem{Title: "clip", URL: "https://x/y/clip.mkv"}, true},
		{"URL percent-encoded", MediaItem{Title: "clip", URL: "https://x/my%20clip.webm"}, true},
		{"URL query ignored", MediaItem{Title: "clip", URL: "https://x/d?file=clip.mp4"}, false},
		{"extensionless with media MIME", MediaItem{Title: "goonbox_1", MimeType: "image/jpeg"}, true},
		{"extensionless octet-stream", MediaItem{Title: "61567794", URL: "https://dl.bunkr.cr/file/61567794", MimeType: "application/octet-stream"}, false},
		{"executable", MediaItem{Title: "setup.exe"}, false},
		{"html", MediaItem{Title: "page.html"}, false},
		{"svg is active content", MediaItem{Title: "icon.svg"}, false},
		{"pdf", MediaItem{Title: "doc.pdf"}, false},
		{"script", MediaItem{Title: "run.js"}, false},
		{"extension wins over lying MIME", MediaItem{Title: "setup.exe", MimeType: "video/mp4"}, false},
		{"empty item", MediaItem{}, false},
		{"youtube-style title", MediaItem{Title: "Some Video", URL: "https://youtube.com/watch?v=x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAllowedMediaItem(tt.item); got != tt.want {
				t.Fatalf("IsAllowedMediaItem(%+v) = %v, want %v", tt.item, got, tt.want)
			}
		})
	}
}

func TestFilterMediaItems(t *testing.T) {
	items := []MediaItem{
		{Title: "a.mp4"},
		{Title: "b.exe"},
		{Title: "c.zip"},
		{Title: "d"},
	}
	kept, skipped := FilterMediaItems(items)
	if skipped != 2 {
		t.Fatalf("skipped = %d, want 2", skipped)
	}
	if len(kept) != 2 || kept[0].Title != "a.mp4" || kept[1].Title != "c.zip" {
		t.Fatalf("kept = %+v, want a.mp4 then c.zip", kept)
	}
	if kept, skipped := FilterMediaItems(nil); len(kept) != 0 || skipped != 0 {
		t.Fatalf("empty input = %d kept, %d skipped", len(kept), skipped)
	}
}

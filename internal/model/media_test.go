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
		{"rar volume r00", MediaItem{Title: "set.r00"}, true},
		{"rar volume r99", MediaItem{Title: "set.r99"}, true},
		{"zip volume z01", MediaItem{Title: "set.z01"}, true},
		{"7z split 001", MediaItem{Title: "set.7z.001"}, true},
		{"zip split 001", MediaItem{Title: "set.zip.001"}, true},
		{"bare split 001", MediaItem{Title: "set.001"}, true},
		{"two-digit suffix is not a volume", MediaItem{Title: "set.12"}, false},
		{"four-digit suffix is not a volume", MediaItem{Title: "set.0001"}, false},
		{"single-digit rar suffix is not a volume", MediaItem{Title: "set.r0"}, false},
		{"tar bz2", MediaItem{Title: "set.tbz2"}, true},
		{"tar xz", MediaItem{Title: "set.txz"}, true},
		{"iso image", MediaItem{Title: "disc.iso"}, true},
		{"tif image", MediaItem{Title: "scan.tif"}, true},
		{"heif image", MediaItem{Title: "photo.heif"}, true},
		{"m4b audiobook", MediaItem{Title: "book.m4b"}, true},
		{"aiff audio", MediaItem{Title: "song.aiff"}, true},
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

func TestMediaFilterExtraExtensions(t *testing.T) {
	filter := NewMediaFilter([]string{"cbz", ".CBR", " iso ", ""})
	tests := []struct {
		name string
		item MediaItem
		want bool
	}{
		{"extra extension", MediaItem{Title: "comic.cbz"}, true},
		{"extra extension case-insensitive", MediaItem{Title: "comic.CBR"}, true},
		{"extra extension with dot normalized", MediaItem{Title: "disc.iso"}, true},
		{"default still allowed", MediaItem{Title: "clip.mp4"}, true},
		{"unknown still rejected", MediaItem{Title: "tool.exe"}, false},
		{"extensionless extra has no MIME rescue", MediaItem{Title: "comic"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filter.IsAllowed(tt.item); got != tt.want {
				t.Fatalf("IsAllowed(%+v) = %v, want %v", tt.item, got, tt.want)
			}
		})
	}

	kept, skipped := filter.Filter([]MediaItem{{Title: "comic.cbz"}, {Title: "tool.exe"}})
	if len(kept) != 1 || skipped != 1 {
		t.Fatalf("Filter() = %d kept, %d skipped, want 1/1", len(kept), skipped)
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

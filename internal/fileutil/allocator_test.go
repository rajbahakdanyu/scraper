package fileutil

import "testing"

func TestPathAllocatorAddsSuffixForCollisions(t *testing.T) {
	allocator := NewPathAllocator()

	if got := allocator.Claim("/tmp/video.mp4", "item-1"); got != "/tmp/video.mp4" {
		t.Fatalf("first claim = %q", got)
	}
	if got := allocator.Claim("/tmp/video.mp4", "item-2"); got != "/tmp/video (2).mp4" {
		t.Fatalf("second claim = %q", got)
	}
	if got := allocator.Claim("/tmp/VIDEO.mp4", "item-3"); got != "/tmp/VIDEO (3).mp4" {
		t.Fatalf("case-insensitive claim = %q", got)
	}
}

func TestPathAllocatorReturnsSamePathToSameOwner(t *testing.T) {
	allocator := NewPathAllocator()
	first := allocator.Claim("/tmp/video.mp4", "item-1")
	second := allocator.Claim("/tmp/video.mp4", "item-1")
	if second != first {
		t.Fatalf("same owner received %q then %q", first, second)
	}
}

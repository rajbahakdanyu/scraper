package fileutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDirCreatesNestedAndRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	if err := EnsureDir(filepath.Join(root, "a", "b")); err != nil {
		t.Fatalf("EnsureDir nested: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "a"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := EnsureDir(link); err == nil || !strings.Contains(err.Error(), "symlinked") {
		t.Fatalf("EnsureDir on a symlink = %v, want refusal", err)
	}
}

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

func TestSafeJoinRejectsTraversal(t *testing.T) {
	for _, component := range []string{"..", "../escape", `..\escape`, "/absolute"} {
		if _, err := SafeJoin(t.TempDir(), component); err == nil {
			t.Errorf("SafeJoin accepted %q", component)
		}
	}
}

func TestSafeJoinReturnsContainedAbsolutePath(t *testing.T) {
	root := t.TempDir()
	got, err := SafeJoin(root, "album-123")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "album-123")
	if got != want {
		t.Fatalf("SafeJoin = %q, want %q", got, want)
	}
}

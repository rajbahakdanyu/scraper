//go:build unix

package disk

import "testing"

func TestFreeBytesClampsNegativeBlocks(t *testing.T) {
	if got := freeBytes(-1, 4096); got != 0 {
		t.Fatalf("freeBytes(-1, 4096) = %d, want 0", got)
	}
	if got := freeBytes(2, 4096); got != 2*4096 {
		t.Fatalf("freeBytes(2, 4096) = %d, want %d", got, 2*4096)
	}
}

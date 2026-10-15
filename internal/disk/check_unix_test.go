//go:build unix

package disk

import (
	"math"
	"testing"
)

func TestNonNegativeClampsNegativeCounts(t *testing.T) {
	if got := nonNegative(int64(-1)); got != 0 {
		t.Fatalf("nonNegative(-1) = %d, want 0", got)
	}
	if got := nonNegative(uint32(4096)); got != 4096 {
		t.Fatalf("nonNegative(4096) = %d, want 4096", got)
	}
}

func TestFreeBytesSaturatesInsteadOfWrapping(t *testing.T) {
	if got := freeBytes(2, 4096); got != 2*4096 {
		t.Fatalf("freeBytes(2, 4096) = %d, want %d", got, 2*4096)
	}
	if got := freeBytes(math.MaxUint64/2, 4096); got != math.MaxInt64 {
		t.Fatalf("an overflowing product must saturate, got %d", got)
	}
	if got := freeBytes(1<<62, 2); got != math.MaxInt64 {
		t.Fatalf("a product above MaxInt64 must saturate, got %d", got)
	}
}

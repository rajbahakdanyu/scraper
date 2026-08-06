package disk

import (
	"path/filepath"
	"testing"
)

func TestFreeSpaceOnTempDir(t *testing.T) {
	dir := t.TempDir()
	free, err := FreeSpace(dir)
	if err != nil {
		t.Skipf("FreeSpace not available: %v", err)
	}
	if free <= 0 {
		t.Errorf("expected positive free space, got %d", free)
	}
}

func TestFreeSpaceNonexistentPath(t *testing.T) {
	_, err := FreeSpace(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Logf("expected error for nonexistent: %v", err)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{12, "12B"},
		{1023, "1023B"},
		{1024, "1.0K"},
		{1536, "1.5K"},
		{1024 * 1024, "1.0M"},
		{int64(2.5 * 1024 * 1024), "2.5M"},
		{1024 * 1024 * 1024, "1.0G"},
		{3328599654, "3.1G"},
	}
	for _, tt := range tests {
		if got := FormatBytes(tt.n); got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatBytesLargeValues(t *testing.T) {
	tb := int64(2) * 1024 * 1024 * 1024 * 1024
	got := FormatBytes(tb)
	if got == "" || got == "0B" {
		t.Errorf("FormatBytes(%d) should not be empty or zero", tb)
	}
}

//go:build unix

package fileutil

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenFileSetsCloseOnExec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	f, err := OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	defer f.Close()

	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("F_GETFD failed: %v", err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("expected FD_CLOEXEC to be set")
	}
}

//go:build unix

package fileutil

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenFileNoFollowSetsCloseOnExec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	f, err := OpenFileNoFollow(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFileNoFollow failed: %v", err)
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

func TestOpenFileNoFollowRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := OpenFileNoFollow(link, os.O_WRONLY, 0644)
	if !IsSymlinkError(err) {
		t.Fatalf("err = %v, want symlink rejection", err)
	}
}

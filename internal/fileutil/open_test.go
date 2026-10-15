package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func TestOpenFileRejectsSymlinkInsideDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, "target.txt", filepath.Join(dir, "link.txt"))
	if _, err := OpenFile(filepath.Join(dir, "link.txt"), os.O_WRONLY, 0600); !errors.Is(err, errSymlink) {
		t.Fatalf("err = %v, want symlink rejection", err)
	}
}

func TestOpenFileRejectsSymlinkOutOfDir(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	symlinkOrSkip(t, victim, filepath.Join(dir, "link.txt"))
	if _, err := OpenFile(filepath.Join(dir, "link.txt"), os.O_CREATE|os.O_WRONLY, 0600); err == nil {
		t.Fatal("a symlink leaving the directory must be refused")
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("the symlink target must not be created, stat err = %v", err)
	}
}

func TestOpenFileReportsCallerPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := Open(path)
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Path != path {
		t.Fatalf("err = %v, want a PathError for %q", err, path)
	}
}

func TestReadFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.txt")
	f, err := OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
}

func TestReadUserFileFollowsSymlinks(t *testing.T) {
	target := filepath.Join(t.TempDir(), "dotfiles.yaml")
	if err := os.WriteFile(target, []byte("output_dir: x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), ".scraper.yaml")
	symlinkOrSkip(t, target, link)
	got, err := ReadUserFile(link)
	if err != nil || string(got) != "output_dir: x" {
		t.Fatalf("ReadUserFile = %q, %v", got, err)
	}
}

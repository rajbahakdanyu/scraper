package fileutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

var errSymlink = errors.New("refusing to follow symlink")

// OpenFile opens a file the program owns (downloads, temp parts, sidecars,
// logs, retry lists). It goes through an os.Root on the parent directory,
// so the open can never resolve outside that directory, and it refuses a
// final component that is a symlink at all.
func OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	if fi, err := root.Lstat(name); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: errSymlink}
	}
	f, err := root.OpenFile(name, flag, perm)
	return f, withPath(err, path)
}

// Open opens an owned file read-only (see OpenFile).
func Open(path string) (*os.File, error) {
	return OpenFile(path, os.O_RDONLY, 0)
}

// ReadFile reads an owned file (see OpenFile).
func ReadFile(path string) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// OpenUserFile opens, read-only, a file the user pointed at (config,
// cookies, URL lists, browser profiles). Their symlinks are followed on
// purpose — dotfiles are often symlinked — and the resolved file is then
// opened through an os.Root on its own directory.
func OpenUserFile(path string) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.Base(resolved))
	return f, withPath(err, path)
}

// ReadUserFile reads a file the user pointed at (see OpenUserFile).
func ReadUserFile(path string) ([]byte, error) {
	f, err := OpenUserFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// withPath reports errors against the caller's path rather than the
// root-relative name.
func withPath(err error, path string) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		pe.Path = path
	}
	return err
}

//go:build !unix

package fileutil

import (
	"errors"
	"os"
)

var errSymlink = errors.New("refusing to follow symlink for .part file")

// OpenFileNoFollow opens a file without traversing symbolic links.
// On platforms that lack O_NOFOLLOW, uses Lstat + Stat to detect and reject symlinks.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	fi, err := os.Lstat(path)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, errSymlink
	}
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	fi2, err2 := f.Stat()
	if err2 == nil && fi2.Mode()&os.ModeSymlink != 0 {
		f.Close()
		return nil, errSymlink
	}
	return f, nil
}

// IsSymlinkError reports whether err is caused by a symlink rejection.
func IsSymlinkError(err error) bool {
	return errors.Is(err, errSymlink)
}

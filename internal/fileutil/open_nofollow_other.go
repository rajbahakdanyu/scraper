//go:build !unix

package fileutil

import (
	"errors"
	"os"
)

var errSymlink = errors.New("refusing to follow symlink")

// OpenFileNoFollow opens a file without traversing symbolic links.
// On platforms that lack O_NOFOLLOW this is best-effort only: Lstat rejects
// a path that is already a symlink before the open, but a link swapped in
// between the Lstat and the open (TOCTOU) cannot be detected afterwards —
// os.File.Stat() on the returned handle resolves the target and never
// reports ModeSymlink. Callers needing a hard guarantee on such platforms
// must open with platform-specific reparse-point flags.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	fi, err := os.Lstat(path)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: errSymlink}
	}
	return os.OpenFile(path, flag, perm)
}

// IsSymlinkError reports whether err is caused by a symlink rejection.
func IsSymlinkError(err error) bool {
	return errors.Is(err, errSymlink)
}

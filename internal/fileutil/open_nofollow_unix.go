//go:build unix

package fileutil

import (
	"errors"
	"os"
	"syscall"
)

var errSymlink = errors.New("refusing to follow symlink")

// OpenFileNoFollow opens a file with O_NOFOLLOW on unix systems,
// refusing to traverse symbolic links.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	// #nosec G304 -- callers pass internally allocated paths; this helper only adds O_NOFOLLOW.
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
	if errors.Is(err, syscall.ELOOP) {
		return nil, &os.PathError{Op: "open", Path: path, Err: errSymlink}
	}
	return f, err
}

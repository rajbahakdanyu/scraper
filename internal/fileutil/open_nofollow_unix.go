//go:build unix

package fileutil

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errSymlink = errors.New("refusing to follow symlink for .part file")

// OpenFileNoFollow opens a file with O_NOFOLLOW on unix systems,
// refusing to traverse symbolic links.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, flag|unix.O_NOFOLLOW, uint32(perm.Perm()))
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, errSymlink
		}
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// IsSymlinkError reports whether err is an error caused by an attempt to
// open a symbolic link when O_NOFOLLOW was requested.
func IsSymlinkError(err error) bool {
	return errors.Is(err, errSymlink)
}

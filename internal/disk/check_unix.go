//go:build unix

package disk

import "golang.org/x/sys/unix"

func FreeSpace(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// #nosec G115 -- disk free space from statfs is bounded by filesystem size, well below int64 max on all practical systems.
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

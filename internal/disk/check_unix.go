//go:build unix

package disk

import "golang.org/x/sys/unix"

func FreeSpace(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// #nosec G115 -- disk free space from statfs is bounded by filesystem size, well below int64 max on all practical systems.
	// Use uint64 multiplication then cap to avoid overflow in the int64 cast.
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	const maxInt64 = uint64(1<<63 - 1)
	if free > maxInt64 {
		free = maxInt64
	}
	return int64(free), nil
}

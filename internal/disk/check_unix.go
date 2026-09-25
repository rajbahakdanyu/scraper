//go:build unix

package disk

import "golang.org/x/sys/unix"

func FreeSpace(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return freeBytes(int64(stat.Bavail), uint64(stat.Bsize)), nil
}

// freeBytes converts a signed block count to bytes. Bavail is signed on
// darwin and can legitimately be negative on an odd filesystem; clamping
// first prevents the uint64 conversion from wrapping it to ~9.2 EB.
func freeBytes(blocks int64, blockSize uint64) int64 {
	if blocks < 0 {
		blocks = 0
	}
	free := uint64(blocks) * blockSize
	const maxInt64 = uint64(1<<63 - 1)
	if free > maxInt64 {
		free = maxInt64
	}
	return int64(free)
}

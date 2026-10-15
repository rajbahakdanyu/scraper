//go:build unix

package disk

import (
	"math"
	"math/bits"

	"golang.org/x/sys/unix"
)

func FreeSpace(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return freeBytes(nonNegative(stat.Bavail), nonNegative(stat.Bsize)), nil
}

// statfsInt covers the statfs field types, which differ between unix
// platforms (Bavail is signed on some, Bsize is 32-bit on others).
type statfsInt interface {
	~int32 | ~int64 | ~uint32 | ~uint64
}

// nonNegative widens a statfs count to uint64, treating a negative value
// (possible on an odd filesystem) as zero.
func nonNegative[T statfsInt](v T) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// freeBytes multiplies blocks by block size, saturating at MaxInt64
// instead of wrapping.
func freeBytes(blocks, blockSize uint64) int64 {
	hi, lo := bits.Mul64(blocks, blockSize)
	if hi != 0 || lo > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(lo)
}

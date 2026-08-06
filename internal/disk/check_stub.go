//go:build !unix

package disk

import "fmt"

func FreeSpace(path string) (int64, error) {
	return 0, fmt.Errorf("disk space check not supported on this platform")
}

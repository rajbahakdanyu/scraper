//go:build !unix

package disk

func FreeSpace(path string) (int64, error) {
	return 0, nil
}

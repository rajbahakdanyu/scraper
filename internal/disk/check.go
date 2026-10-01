package disk

import (
	"fmt"
	"os"
	"path/filepath"
)

// FreeSpaceNearestExisting reports free space for the closest existing
// ancestor of path, so a check against a directory that does not exist
// yet (the usual first-run album folder) still sees the filesystem it
// will be created on. Only missing ancestors are walked past; any other
// statfs error is returned as-is.
func FreeSpaceNearestExisting(path string) (int64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	for {
		free, statErr := FreeSpace(abs)
		if statErr == nil {
			return free, nil
		}
		if !os.IsNotExist(statErr) {
			return 0, statErr
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return 0, fmt.Errorf("no existing ancestor of %q to stat", path)
		}
		abs = parent
	}
}

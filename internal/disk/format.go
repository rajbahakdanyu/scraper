package disk

import "github.com/rajbahakdanyu/scraper/internal/fileutil"

func FormatBytes(n int64) string {
	return fileutil.HumanBytes(n)
}

package downloader

import (
	"fmt"
	"os"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func itemIdentity(item model.MediaItem) string {
	source := item.PageURL
	if source == "" {
		source = item.URL
	}
	return fileutil.SourceIdentity(fmt.Sprintf("%s\n%s\n%s", source, item.URL, item.Type))
}

func resumablePartial(path string, item model.MediaItem, identity string) bool {
	tempPath := path + ".part"
	info, err := os.Stat(tempPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false
	}
	metadata, err := readResumeMetadata(tempPath)
	if err != nil || metadata.validator() == "" {
		return false
	}
	if metadata.ItemIdentity != "" {
		return metadata.ItemIdentity == identity
	}
	return item.Resolve == nil && metadata.URL == item.URL
}

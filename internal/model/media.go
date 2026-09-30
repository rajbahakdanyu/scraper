package model

import (
	"net/url"
	"path"
	"strings"
)

// allowedMediaExtensions is the default --media-only allowlist: common media
// containers plus archives. Names are lowercase and carry no dot.
var allowedMediaExtensions = map[string]struct{}{
	// video
	"mp4": {}, "m4v": {}, "mkv": {}, "webm": {}, "avi": {}, "mov": {},
	"wmv": {}, "flv": {}, "mpg": {}, "mpeg": {}, "ts": {}, "m2ts": {},
	"3gp": {}, "ogv": {},
	// audio
	"mp3": {}, "m4a": {}, "aac": {}, "flac": {}, "wav": {}, "ogg": {},
	"opus": {}, "wma": {}, "m4b": {}, "aiff": {},
	// image
	"jpg": {}, "jpeg": {}, "png": {}, "gif": {}, "webp": {}, "bmp": {},
	"avif": {}, "heic": {}, "heif": {}, "tiff": {}, "tif": {},
	// archives (split-volume companions are matched by isVolumeExtension)
	"zip": {}, "rar": {}, "7z": {}, "tar": {}, "gz": {}, "tgz": {},
	"bz2": {}, "xz": {}, "tbz2": {}, "txz": {}, "iso": {},
}

// isVolumeExtension reports whether an extension names a split-archive
// volume rather than a file type: .r00-.r99 (RAR), .z01-.z99 (Zip), and
// .001-.999 (7-Zip / Zip split parts). Without these, a media-only run
// keeps one volume of a set and the archive cannot be extracted.
func isVolumeExtension(ext string) bool {
	if len(ext) != 3 {
		return false
	}
	if ext[0] == 'r' || ext[0] == 'z' {
		ext = ext[1:]
	}
	for _, r := range ext {
		if r < '0' || r > '9' {
			return false
		}
	}
	return ext != ""
}

// MediaFilter applies the --media-only allowlist: the built-in media and
// archive set, split-archive volume patterns, and any user-configured extra
// extensions (--allow-ext / media_extensions).
type MediaFilter struct {
	extra map[string]struct{}
}

// NewMediaFilter builds a filter with extra extensions appended to the
// built-in allowlist. Extras are normalized (trimmed, dot-stripped,
// lowercased); blanks are ignored.
func NewMediaFilter(extra []string) MediaFilter {
	filter := MediaFilter{}
	if len(extra) == 0 {
		return filter
	}
	filter.extra = make(map[string]struct{}, len(extra))
	for _, ext := range extra {
		ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
		if ext == "" {
			continue
		}
		filter.extra[ext] = struct{}{}
	}
	return filter
}

// IsAllowed reports whether an item's filename marks it as media or an
// archive. The title is checked first (parsers put the real filename
// there), then the URL path; only when neither yields an extension is the
// API-provided MIME prefix used as a fallback. A present-but-disallowed
// extension is never rescued by MIME, and unknown or extensionless items
// without a media MIME are rejected.
func (f MediaFilter) IsAllowed(item MediaItem) bool {
	if ext := itemExtension(item); ext != "" {
		if _, ok := allowedMediaExtensions[ext]; ok {
			return true
		}
		if isVolumeExtension(ext) {
			return true
		}
		_, ok := f.extra[ext]
		return ok
	}
	mime := strings.ToLower(strings.TrimSpace(item.MimeType))
	return strings.HasPrefix(mime, "video/") ||
		strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "image/")
}

// Filter drops items that are not media or archives, preserving order, and
// reports how many were skipped.
func (f MediaFilter) Filter(items []MediaItem) ([]MediaItem, int) {
	kept := make([]MediaItem, 0, len(items))
	for _, item := range items {
		if f.IsAllowed(item) {
			kept = append(kept, item)
		}
	}
	return kept, len(items) - len(kept)
}

// IsAllowedMediaItem reports whether an item's filename marks it as media or
// an archive, using the default allowlist.
func IsAllowedMediaItem(item MediaItem) bool {
	return MediaFilter{}.IsAllowed(item)
}

// itemExtension returns the lowercase extension (without the dot) from the
// item's title, falling back to the URL's path. Both may be absent.
func itemExtension(item MediaItem) string {
	if ext := extensionOf(item.Title); ext != "" {
		return ext
	}
	raw := item.URL
	if u, err := url.Parse(raw); err == nil {
		raw = u.Path
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	return extensionOf(raw)
}

func extensionOf(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
}

// FilterMediaItems drops items that are not media or archives, preserving
// order, and reports how many were skipped, using the default allowlist.
func FilterMediaItems(items []MediaItem) ([]MediaItem, int) {
	return MediaFilter{}.Filter(items)
}

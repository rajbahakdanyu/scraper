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
	"opus": {}, "wma": {},
	// image
	"jpg": {}, "jpeg": {}, "png": {}, "gif": {}, "webp": {}, "bmp": {},
	"avif": {}, "heic": {}, "tiff": {},
	// archives
	"zip": {}, "rar": {}, "7z": {}, "tar": {}, "gz": {}, "tgz": {},
	"bz2": {}, "xz": {},
}

// IsAllowedMediaItem reports whether an item's filename marks it as media or
// an archive. The title is checked first (parsers put the real filename
// there), then the URL path; only when neither yields an extension is the
// API-provided MIME prefix used as a fallback. A present-but-disallowed
// extension is never rescued by MIME, and unknown or extensionless items
// without a media MIME are rejected.
func IsAllowedMediaItem(item MediaItem) bool {
	if ext := itemExtension(item); ext != "" {
		_, ok := allowedMediaExtensions[ext]
		return ok
	}
	mime := strings.ToLower(strings.TrimSpace(item.MimeType))
	return strings.HasPrefix(mime, "video/") ||
		strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "image/")
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
// order, and reports how many were skipped.
func FilterMediaItems(items []MediaItem) ([]MediaItem, int) {
	kept := make([]MediaItem, 0, len(items))
	for _, item := range items {
		if IsAllowedMediaItem(item) {
			kept = append(kept, item)
		}
	}
	return kept, len(items) - len(kept)
}

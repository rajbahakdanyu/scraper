package netutil

import "strings"

// PathSlug returns the final slash-delimited segment of rawURL without its
// query string or trailing slashes.
func PathSlug(rawURL string) string {
	if idx := strings.IndexByte(rawURL, '?'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

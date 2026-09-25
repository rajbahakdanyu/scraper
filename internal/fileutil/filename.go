package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	// maxFilenameBytes is the common per-component limit on Linux (ext4)
	// and the practical limit on macOS/Windows.
	maxFilenameBytes = 255
	// filenameSuffixReserve leaves room for suffixes added after
	// sanitization: the downloader's ".part" + ".resume.json" (16 bytes),
	// a PathAllocator collision suffix like " (99)" (5 bytes), and the
	// ".sha256" sidecar (7 bytes). Returning a full 255-byte name would
	// make the .part sibling exceed the filesystem limit.
	filenameSuffixReserve = 24
	filenameBudget        = maxFilenameBytes - filenameSuffixReserve
	filenameHashLen       = 8
)

// SanitizeFilename removes characters that are invalid in filenames, strips
// trailing dots/spaces (Windows silently drops them), guards Windows
// reserved device names, and clamps the result to filenameBudget (255 bytes
// minus room for the downloader's own suffixes). It also rejects "." and
// ".." (once slashes are stripped, these are the only remaining path
// segments that could otherwise let a crafted item title make the joined
// download path escape the destination directory).
func SanitizeFilename(s string) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 32 && r != ':' && r != '<' && r != '>' && r != '|' && r != '/' && r != '\\' && r != '?' && r != '*' {
			clean = append(clean, r)
		}
	}
	result := strings.TrimRight(string(clean), ". ")
	if result == "" || result == "." || result == ".." {
		return ""
	}
	if isWindowsReservedName(result) {
		result = "_" + result
	}
	if len(result) > filenameBudget {
		result = truncateFilenameWithHash(result, s)
	}
	return result
}

// isWindowsReservedName reports whether name's base (before the first dot)
// is a Windows reserved device name. Windows treats "CON", "CON.txt", and
// "con" alike, so the check is case-insensitive and extension-aware.
func isWindowsReservedName(name string) bool {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 {
		prefix, digit := base[:3], base[3]
		if digit >= '1' && digit <= '9' {
			switch strings.ToUpper(prefix) {
			case "COM", "LPT":
				return true
			}
		}
	}
	return false
}

// truncateFilenameWithHash clips name to fit filenameBudget while keeping
// its extension, replacing the removed bytes with "-" + the first 8 hex
// chars of sha256(original) so distinct titles stay distinct. original is
// hashed rather than the cleaned name so the suffix is stable across calls.
func truncateFilenameWithHash(name, original string) string {
	sum := sha256.Sum256([]byte(original))
	hash := hex.EncodeToString(sum[:])[:filenameHashLen]

	ext := path.Ext(name)
	if len(ext) > filenameBudget/2 {
		ext = "" // a pathological "extension" leaves no room for a stem
	}
	suffix := "-" + hash
	budget := filenameBudget - len(ext) - len(suffix)
	if budget < 1 {
		ext = ""
		budget = filenameBudget - len(suffix)
	}
	body := name[:len(name)-len(ext)]
	if len(body) > budget {
		body = truncateUTF8Bytes(body, budget)
	}
	return body + suffix + ext
}

// truncateUTF8Bytes cuts s to at most max bytes without splitting a rune.
func truncateUTF8Bytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

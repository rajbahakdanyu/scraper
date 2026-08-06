package fileutil

// SanitizeFilename removes characters that are invalid in filenames. It also
// rejects "." and ".." (once slashes are stripped, these are the only
// remaining path segments that could otherwise let a crafted item title make
// the joined download path escape the destination directory).
func SanitizeFilename(s string) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 32 && r != ':' && r != '<' && r != '>' && r != '|' && r != '/' && r != '\\' && r != '?' && r != '*' {
			clean = append(clean, r)
		}
	}
	result := string(clean)
	if result == "" || result == "." || result == ".." {
		return ""
	}
	return result
}

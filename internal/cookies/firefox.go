package cookies

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	// Pure-Go SQLite driver: keeps the CLI CGO-free.
	_ "modernc.org/sqlite"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

// firefoxRootCandidates lists where Firefox keeps profiles.ini on goos, in
// priority order (Linux: native, snap, then flatpak installs).
func firefoxRootCandidates(goos, home, appData string) []string {
	switch goos {
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Firefox")}
	case "windows":
		if appData == "" {
			return nil
		}
		return []string{filepath.Join(appData, "Mozilla", "Firefox")}
	default:
		return []string{
			filepath.Join(home, ".mozilla", "firefox"),
			filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
			filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
		}
	}
}

// firefoxRoot is a test seam for the Firefox profile root directory: the
// first candidate holding a profiles.ini, else the first candidate.
var firefoxRoot = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	candidates := firefoxRootCandidates(runtime.GOOS, home, os.Getenv("APPDATA"))
	if len(candidates) == 0 {
		return "", fmt.Errorf("no Firefox profile location known for %s", runtime.GOOS)
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "profiles.ini")); err == nil {
			return dir, nil
		}
	}
	return candidates[0], nil
}

// Import loads cookies for domains from a local browser profile. Only
// Firefox is supported; Chrome on macOS encrypts its cookie store behind the
// Keychain (a separate decision). Cookies are filtered to domains, and the
// client's cookie jar is host-scoped, so unrelated cookies are never sent
// anywhere.
func Import(browser string, domains []string) ([]*http.Cookie, error) {
	if browser != "firefox" {
		return nil, fmt.Errorf("unsupported browser %q (only firefox is supported)", browser)
	}
	profile, err := firefoxProfileDir()
	if err != nil {
		return nil, err
	}
	return readFirefoxCookies(filepath.Join(profile, "cookies.sqlite"), domains)
}

// firefoxProfileDir finds the default profile directory from profiles.ini,
// falling back to the first listed profile.
func firefoxProfileDir() (string, error) {
	root, err := firefoxRoot()
	if err != nil {
		return "", err
	}
	data, err := fileutil.ReadUserFile(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return "", fmt.Errorf("find Firefox profile: %w", err)
	}

	type profile struct {
		section  string
		path     string
		relative string // "1", "0", or "" (absent: Firefox treats it as relative)
	}
	// Attributes arrive per section in any order (IsRelative can precede
	// Path), so collect them per section first, then build the list in
	// file order.
	var order []string
	sections := map[string]*profile{}
	defaults := map[string]bool{}
	var installDefaults []string
	var current string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.Trim(line, "[]")
			if strings.HasPrefix(current, "Profile") {
				if _, ok := sections[current]; !ok {
					order = append(order, current)
					sections[current] = &profile{section: current}
				}
			}
			continue
		}
		if section := sections[current]; section != nil && strings.HasPrefix(line, "Path=") {
			section.path = strings.TrimPrefix(line, "Path=")
			continue
		}
		if section := sections[current]; section != nil && strings.HasPrefix(line, "IsRelative=") {
			section.relative = strings.TrimPrefix(line, "IsRelative=")
			continue
		}
		if strings.HasPrefix(current, "Install") && strings.HasPrefix(line, "Default=") {
			if value := strings.TrimPrefix(line, "Default="); value != "" {
				installDefaults = append(installDefaults, value)
			}
		} else if sections[current] != nil && line == "Default=1" {
			defaults[current] = true
		}
	}
	if len(order) == 0 {
		return "", fmt.Errorf("no Firefox profiles found in %s", filepath.Join(root, "profiles.ini"))
	}
	profiles := make([]profile, 0, len(order))
	for _, s := range order {
		profiles = append(profiles, *sections[s])
	}
	if len(installDefaults) > 0 {
		candidate := installDefaults[0]
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, filepath.FromSlash(candidate))
		}
		candidate = filepath.Clean(candidate)
		for _, p := range profiles {
			if filepath.Clean(profileDir(root, p.path, p.relative)) == candidate {
				return candidate, nil
			}
		}
		return candidate, nil
	}
	for _, p := range profiles {
		if defaults[p.section] && p.path != "" {
			return profileDir(root, p.path, p.relative), nil
		}
	}
	for _, p := range profiles {
		if p.path != "" {
			return profileDir(root, p.path, p.relative), nil
		}
	}
	return "", fmt.Errorf("no Firefox profile path found in %s", filepath.Join(root, "profiles.ini"))
}

// profileDir resolves a profile record to its directory: relative paths
// sit under the Firefox root; IsRelative=0 means the Path is absolute.
// Firefox writes IsRelative=1 by default; absent means relative.
func profileDir(root string, path, relative string) string {
	if relative == "0" && path != "" {
		return filepath.FromSlash(path)
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

// readFirefoxCookies reads the cookies for domains out of a Firefox
// cookies.sqlite.
func readFirefoxCookies(dbPath string, domains []string) ([]*http.Cookie, error) {
	if len(domains) == 0 {
		return nil, fmt.Errorf("no supported site domains to filter cookies by")
	}
	// A running Firefox holds the live database locked, so query a copy.
	tmp, err := os.MkdirTemp("", "scraper-cookies-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	dbCopy := filepath.Join(tmp, "cookies.sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFile(dbPath+suffix, dbCopy+suffix); err != nil {
			if suffix != "" && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("copy Firefox cookie database: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbCopy)
	if err != nil {
		return nil, fmt.Errorf("open Firefox cookie database: %w", err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT name, value, host, path, expiry FROM moz_cookies")
	if err != nil {
		return nil, fmt.Errorf("read Firefox cookies: %w", err)
	}
	defer rows.Close()

	var cookies []*http.Cookie
	now := time.Now().Unix()
	for rows.Next() {
		var name, value, host, path string
		var expiry int64
		if err := rows.Scan(&name, &value, &host, &path, &expiry); err != nil {
			return nil, fmt.Errorf("read Firefox cookies: %w", err)
		}
		if !hostMatches(host, domains) {
			continue
		}
		// Newer Firefox versions store milliseconds; seconds stay below 1e11
		// until the year 5138.
		if expiry > 1e11 {
			expiry /= 1000
		}
		if expiry > 0 && expiry < now {
			continue
		}
		var expires time.Time
		if expiry > 0 {
			expires = time.Unix(expiry, 0)
		}
		cookies = append(cookies, netutil.NewCookie(name, value, host, path, expires))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Firefox cookies: %w", err)
	}
	return cookies, nil
}

// hostMatches reports whether a cookie host (".bunkr.cr", "cdn.bunkr.cr")
// is one of domains or a subdomain of one; a lookalike such as
// evilbunkr.cr is not.
func hostMatches(host string, domains []string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "."))
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := fileutil.OpenUserFile(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := fileutil.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return errors.Join(err, out.Close())
	}
	return out.Close()
}

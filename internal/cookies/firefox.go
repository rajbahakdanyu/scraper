package cookies

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

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
	var current string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.Trim(line, "[]")
			if _, ok := sections[current]; !ok {
				order = append(order, current)
				sections[current] = &profile{section: current}
			}
			continue
		}
		if current == "" {
			continue
		}
		if strings.HasPrefix(line, "Path=") {
			sections[current].path = strings.TrimPrefix(line, "Path=")
			continue
		}
		if strings.HasPrefix(line, "IsRelative=") {
			sections[current].relative = strings.TrimPrefix(line, "IsRelative=")
			continue
		}
		if line == "Default=1" {
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
	for _, p := range profiles {
		if defaults[p.section] {
			return profileDir(root, p.path, p.relative), nil
		}
	}
	return profileDir(root, profiles[0].path, profiles[0].relative), nil
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

// readFirefoxCookies reads cookies for domains out of a Firefox
// cookies.sqlite using the sqlite3 CLI, avoiding a database driver
// dependency.
func readFirefoxCookies(dbPath string, domains []string) ([]*http.Cookie, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, fmt.Errorf("reading Firefox cookies needs the sqlite3 CLI: %w", err)
	}
	var clauses []string
	for _, domain := range domains {
		// Domains are interpolated into SQL, so only plain hostnames pass.
		if !validDomain(domain) {
			return nil, fmt.Errorf("invalid cookie domain %q", domain)
		}
		// Exact host, dot-prefixed host, or a subdomain. A
		// left-unanchored LIKE ('%bunkr.cr') would also match lookalike
		// hosts like evilbunkr.cr.
		clauses = append(clauses,
			fmt.Sprintf("host = '%s'", domain),
			fmt.Sprintf("host = '.%s'", domain),
			fmt.Sprintf("host LIKE '%%.%s'", domain),
		)
	}
	if len(clauses) == 0 {
		return nil, fmt.Errorf("no supported site domains to filter cookies by")
	}
	query := "SELECT name, value, host, path, expiry FROM moz_cookies WHERE " +
		strings.Join(clauses, " OR ")
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
	// #nosec G204 -- fixed binary, no shell; the query only holds validDomain-checked hostnames.
	out, err := exec.Command("sqlite3", "-separator", "\t", dbCopy, query).Output()
	if err != nil {
		return nil, fmt.Errorf("read Firefox cookies: %w", err)
	}

	var cookies []*http.Cookie
	now := time.Now().Unix()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			continue
		}
		expiry, _ := strconv.ParseInt(fields[4], 10, 64)
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
		cookies = append(cookies, netutil.NewCookie(fields[0], fields[1], fields[2], fields[3], expires))
	}
	return cookies, nil
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

func validDomain(domain string) bool {
	if domain == "" {
		return false
	}
	for _, r := range domain {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

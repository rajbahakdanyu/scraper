package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// firefoxRoot is a test seam for the Firefox profile root directory.
var firefoxRoot = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Firefox"), nil
}

// importBrowserCookies loads cookies for the supported sites from a local
// browser profile. Only Firefox is supported; Chrome on macOS encrypts its
// cookie store behind the Keychain (a separate decision). Cookies are
// filtered to the supported site domains, and the client's cookie jar is
// host-scoped, so unrelated cookies are never sent anywhere.
func importBrowserCookies(browser string) ([]*http.Cookie, error) {
	if browser != "firefox" {
		return nil, fmt.Errorf("unsupported browser %q (only firefox is supported)", browser)
	}
	profile, err := firefoxProfileDir()
	if err != nil {
		return nil, err
	}
	return readFirefoxCookies(filepath.Join(profile, "cookies.sqlite"))
}

// firefoxProfileDir finds the default profile directory from profiles.ini,
// falling back to the first listed profile.
func firefoxProfileDir() (string, error) {
	root, err := firefoxRoot()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return "", fmt.Errorf("find Firefox profile: %w", err)
	}

	type profile struct {
		section string
		path    string
	}
	var profiles []profile
	defaults := map[string]bool{}
	var current string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.Trim(line, "[]")
			continue
		}
		if current == "" {
			continue
		}
		if strings.HasPrefix(line, "Path=") {
			profiles = append(profiles, profile{section: current, path: strings.TrimPrefix(line, "Path=")})
		}
		if line == "Default=1" {
			defaults[current] = true
		}
	}
	if len(profiles) == 0 {
		return "", fmt.Errorf("no Firefox profiles found in %s", filepath.Join(root, "profiles.ini"))
	}
	for _, p := range profiles {
		if defaults[p.section] {
			return filepath.Join(root, filepath.FromSlash(p.path)), nil
		}
	}
	return filepath.Join(root, filepath.FromSlash(profiles[0].path)), nil
}

// readFirefoxCookies reads cookies for the supported site domains out of a
// Firefox cookies.sqlite using the sqlite3 CLI, avoiding a database driver
// dependency.
func readFirefoxCookies(dbPath string) ([]*http.Cookie, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, fmt.Errorf("reading Firefox cookies needs the sqlite3 CLI: %w", err)
	}
	var clauses []string
	for _, domains := range siteDomains {
		for _, domain := range domains {
			clauses = append(clauses, fmt.Sprintf("host LIKE '%%%s'", domain))
		}
	}
	if len(clauses) == 0 {
		return nil, fmt.Errorf("no supported site domains to filter cookies by")
	}
	query := "SELECT name, value, host, path, expiry, isSecure, isHttpOnly FROM moz_cookies WHERE " +
		strings.Join(clauses, " OR ")
	out, err := exec.Command("sqlite3", "-separator", "\t", dbPath, query).Output()
	if err != nil {
		return nil, fmt.Errorf("read Firefox cookies: %w", err)
	}

	var cookies []*http.Cookie
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			continue
		}
		expiry, _ := strconv.ParseInt(fields[4], 10, 64)
		cookie := &http.Cookie{
			Name:     fields[0],
			Value:    fields[1],
			Domain:   fields[2],
			Path:     fields[3],
			Secure:   fields[5] == "1",
			HttpOnly: fields[6] == "1",
		}
		if expiry > 0 {
			cookie.Expires = time.Unix(expiry, 0)
		}
		cookies = append(cookies, cookie)
	}
	return cookies, nil
}

package cookies

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

var testDomains = []string{"bunkr.cr"}

// createCookieDB writes a Firefox-shaped cookies.sqlite with the given
// (name, value, host, expiry) rows.
func createCookieDB(t *testing.T, rows [][4]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE moz_cookies (name TEXT, value TEXT, host TEXT, path TEXT, expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec("INSERT INTO moz_cookies VALUES (?, ?, ?, '/', ?, 0, 0)", r[0], r[1], r[2], r[3]); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func names(cookies []*http.Cookie) map[string]bool {
	got := map[string]bool{}
	for _, c := range cookies {
		got[c.Name] = true
	}
	return got
}

func TestImportBrowserCookiesRejectsUnsupportedBrowser(t *testing.T) {
	if _, err := Import("chrome", nil); err == nil {
		t.Fatal("expected chrome to be rejected")
	}
}

func TestFirefoxProfileDirPrefersDefault(t *testing.T) {
	root := t.TempDir()
	ini := "[Profile0]\nName=dev\nPath=Profiles/aaa.dev\n\n[Profile1]\nName=default\nPath=Profiles/bbb.default\nDefault=1\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0600); err != nil {
		t.Fatal(err)
	}
	prev := firefoxRoot
	firefoxRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { firefoxRoot = prev })

	got, err := firefoxProfileDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "Profiles", "bbb.default"); got != want {
		t.Fatalf("profile = %q, want %q", got, want)
	}
}

func TestReadFirefoxCookiesFiltersToSupportedSites(t *testing.T) {
	db := createCookieDB(t, [][4]any{
		{"sid", "abc", ".bunkr.cr", 2000000000},
		{"sub", "abc", "cdn.bunkr.cr", 2000000000},
		{"lookalike", "zzz", ".evilbunkr.cr", 2000000000},
		{"other", "zzz", ".example.com", 2000000000},
		{"quote", "zzz", "x' OR 1=1 --", 2000000000},
	})

	cookies, err := readFirefoxCookies(db, testDomains)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cookies); len(cookies) != 2 || !got["sid"] || !got["sub"] {
		t.Fatalf("cookies = %+v, want exactly the real-domain rows", cookies)
	}
	for _, c := range cookies {
		if !c.Secure || !c.HttpOnly {
			t.Fatalf("imported cookie %q must be Secure and HttpOnly", c.Name)
		}
	}
}

func TestReadFirefoxCookiesSkipsExpiredAndHandlesMilliseconds(t *testing.T) {
	db := createCookieDB(t, [][4]any{
		{"expired", "x", ".bunkr.cr", 1000000000},
		{"seconds", "x", ".bunkr.cr", 4000000000},
		{"millis", "x", ".bunkr.cr", int64(4000000000000)},
		{"expiredMillis", "x", ".bunkr.cr", int64(1000000000000)},
	})
	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}

	cookies, err := readFirefoxCookies(db, testDomains)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cookies); len(cookies) != 2 || !got["seconds"] || !got["millis"] {
		t.Fatalf("want only the unexpired seconds/millis cookies, got %+v", cookies)
	}
	if after, _ := os.ReadFile(db); string(after) != string(before) {
		t.Fatal("the live Firefox database must not be modified")
	}
}

func TestReadFirefoxCookiesNeedsDomains(t *testing.T) {
	if _, err := readFirefoxCookies(createCookieDB(t, nil), nil); err == nil {
		t.Fatal("expected an error without any domains to filter by")
	}
}

func TestHostMatches(t *testing.T) {
	for host, want := range map[string]bool{
		"bunkr.cr":      true,
		".bunkr.cr":     true,
		"cdn.bunkr.cr":  true,
		"CDN.Bunkr.CR":  true,
		"evilbunkr.cr":  false,
		"bunkr.cr.evil": false,
	} {
		if got := hostMatches(host, testDomains); got != want {
			t.Errorf("hostMatches(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestFirefoxRootCandidates(t *testing.T) {
	if got := firefoxRootCandidates("darwin", "/h", ""); len(got) != 1 || got[0] != filepath.Join("/h", "Library", "Application Support", "Firefox") {
		t.Fatalf("darwin = %v", got)
	}
	linux := firefoxRootCandidates("linux", "/h", "")
	if len(linux) != 3 || linux[0] != filepath.Join("/h", ".mozilla", "firefox") {
		t.Fatalf("linux = %v", linux)
	}
	if got := firefoxRootCandidates("windows", "/h", `C:\AppData`); len(got) != 1 || got[0] != filepath.Join(`C:\AppData`, "Mozilla", "Firefox") {
		t.Fatalf("windows = %v", got)
	}
	if got := firefoxRootCandidates("windows", "/h", ""); len(got) != 0 {
		t.Fatalf("windows without APPDATA = %v", got)
	}
}

func TestFirefoxProfileDirHonorsAbsoluteProfilePath(t *testing.T) {
	root := t.TempDir()
	ini := "[Profile0]\nName=moved\nIsRelative=0\nPath=/opt/firefox-profiles/me.default\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0600); err != nil {
		t.Fatal(err)
	}
	prev := firefoxRoot
	firefoxRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { firefoxRoot = prev })

	got, err := firefoxProfileDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := "/opt/firefox-profiles/me.default"; got != want {
		t.Fatalf("absolute profile path mangled: got %q, want %q", got, want)
	}
}

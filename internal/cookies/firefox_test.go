package cookies

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var testDomains = []string{"bunkr.cr"}

func TestReadFirefoxCookiesRejectsUnsafeDomain(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	if _, err := readFirefoxCookies(filepath.Join(t.TempDir(), "missing"), []string{"x' OR 1=1 --"}); err == nil {
		t.Fatal("expected a domain with SQL metacharacters to be rejected")
	}
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
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "cookies.sqlite")
	create := exec.Command("sqlite3", db,
		"CREATE TABLE moz_cookies (name TEXT, value TEXT, host TEXT, path TEXT, expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER);"+
			"INSERT INTO moz_cookies VALUES ('sid','abc','.bunkr.cr','/',2000000000,1,1);"+
			"INSERT INTO moz_cookies VALUES ('sub','abc','cdn.bunkr.cr','/',2000000000,1,1);"+
			"INSERT INTO moz_cookies VALUES ('lookalike','zzz','.evilbunkr.cr','/',2000000000,0,0);"+
			"INSERT INTO moz_cookies VALUES ('other','zzz','.example.com','/',2000000000,0,0);")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create cookies db: %v: %s", err, out)
	}

	cookies, err := readFirefoxCookies(db, testDomains)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 2 {
		t.Fatalf("cookies = %+v, want exactly the real-domain rows (lookalike evilbunkr.cr excluded)", cookies)
	}
	for _, c := range cookies {
		if c.Name == "lookalike" || c.Name == "other" {
			t.Fatalf("unrelated/lookalike cookie %q must not be imported", c.Name)
		}
	}
}

func TestReadFirefoxCookiesSkipsExpiredAndHandlesMilliseconds(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "cookies.sqlite")
	create := exec.Command("sqlite3", db,
		"CREATE TABLE moz_cookies (name TEXT, value TEXT, host TEXT, path TEXT, expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER);"+
			"INSERT INTO moz_cookies VALUES ('expired','x','.bunkr.cr','/',1000000000,1,1);"+
			"INSERT INTO moz_cookies VALUES ('seconds','x','.bunkr.cr','/',4000000000,1,1);"+
			"INSERT INTO moz_cookies VALUES ('millis','x','.bunkr.cr','/',4000000000000,1,1);"+
			"INSERT INTO moz_cookies VALUES ('expiredMillis','x','.bunkr.cr','/',1000000000000,1,1);")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create cookies db: %v: %s", err, out)
	}
	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}

	cookies, err := readFirefoxCookies(db, testDomains)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range cookies {
		got[c.Name] = true
	}
	if len(cookies) != 2 || !got["seconds"] || !got["millis"] {
		t.Fatalf("want only the unexpired seconds/millis cookies, got %+v", cookies)
	}
	if after, _ := os.ReadFile(db); string(after) != string(before) {
		t.Fatal("the live Firefox database must not be modified")
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

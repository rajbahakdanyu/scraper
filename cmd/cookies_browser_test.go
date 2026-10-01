package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestImportBrowserCookiesRejectsUnsupportedBrowser(t *testing.T) {
	if _, err := importBrowserCookies("chrome"); err == nil {
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

	cookies, err := readFirefoxCookies(db)
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

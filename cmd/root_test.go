package cmd

import "testing"

func TestNewRootCmdRegistersSubcommands(t *testing.T) {
	root := NewRootCmd()

	want := []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"}
	for _, name := range want {
		found := false
		for _, c := range root.Commands() {
			if c.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected root command to register %q subcommand", name)
		}
	}

	if root.PersistentFlags().Lookup("config") == nil {
		t.Error("expected --config persistent flag")
	}
	if root.PersistentFlags().Lookup("output") == nil {
		t.Error("expected --output persistent flag")
	}
	if root.PersistentFlags().Lookup("concurrent") == nil {
		t.Error("expected --concurrent persistent flag")
	}
}

func TestDetectSite(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://youtube.com/watch?v=abc", "youtube"},
		{"https://www.youtube.com/watch?v=abc", "youtube"},
		{"https://youtu.be/abc", "youtube"},
		{"https://cyberdrop.cr/a/abc", "cyberdrop"},
		{"https://cyberdrop.me/a/abc", "cyberdrop"},
		{"https://cyberdrop.to/a/abc", "cyberdrop"},
		{"https://bunkr.cr/a/abc", "bunker"},
		{"https://bunkr.su/a/abc", "bunker"},
		{"https://bunkrr.su/a/abc", "bunker"},
		{"https://bunkr.la/a/abc", "bunker"},
		{"https://bunkr.ph/a/abc", "bunker"},
		{"https://goonbox.cr/a/abc", "goonbox"},
		{"https://goonbox.to/a/abc", "goonbox"},
		{"https://filester.sh/f/abc", "filester"},
		{"https://filester.me/d/abc", "filester"},
		{"https://filester.sh:8443/f/abc", "filester"},
	}

	for _, tt := range tests {
		got, err := detectSite(tt.url)
		if err != nil {
			t.Errorf("detectSite(%s) returned error: %v", tt.url, err)
			continue
		}
		if got != tt.want {
			t.Errorf("detectSite(%s) = %s, want %s", tt.url, got, tt.want)
		}
	}
}

func TestDetectSiteRejectsLookalikeDomains(t *testing.T) {
	// These contain a known site's name as a substring but aren't actually
	// that site, so they must not match (a plain strings.Contains check
	// on the host would incorrectly match these).
	lookalikes := []string{
		"https://notcyberdrop.example.com/a/abc",
		"https://cyberdrop.example.com/a/abc",
		"https://evil-bunkr.com/a/abc",
		"https://filester.sh.evil.com/f/abc",
	}
	for _, u := range lookalikes {
		if got, err := detectSite(u); err == nil {
			t.Errorf("detectSite(%s) = %s, want an error for a lookalike domain", u, got)
		}
	}
}

func TestDetectSiteUnknown(t *testing.T) {
	if _, err := detectSite("https://example.com/whatever"); err == nil {
		t.Error("expected error for an unrecognized domain")
	}
	if _, err := detectSite("not a url"); err == nil {
		t.Error("expected error for an unparseable URL")
	}
}

func TestRootCmdNoArgsShowsHelp(t *testing.T) {
	root := NewRootCmd()
	if err := root.RunE(root, nil); err != nil {
		t.Errorf("expected bare invocation to show help without error, got: %v", err)
	}
}

func TestRootCmdAutoDetectsUnknownDomain(t *testing.T) {
	root := NewRootCmd()
	if err := root.RunE(root, []string{"https://example.com/whatever"}); err == nil {
		t.Error("expected error for an unrecognized domain")
	}
}

func TestScraperRegistry(t *testing.T) {
	reg := scraperRegistry()
	for _, name := range []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"} {
		if _, ok := reg[name]; !ok {
			t.Errorf("expected registry to contain %q", name)
		}
	}
}

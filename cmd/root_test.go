package cmd

import "testing"

func TestNewRootCmdRegistersSubcommands(t *testing.T) {
	root := NewRootCmd()

	want := []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester", "tui"}
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
		{"https://bunkr.cr/a/abc", "bunker"},
		{"https://goonbox.cr/a/abc", "goonbox"},
		{"https://filester.sh/f/abc", "filester"},
		{"https://filester.me/d/abc", "filester"},
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

package cmd

import "testing"

func TestNewTUICmd(t *testing.T) {
	opts := globalOptions{}
	cmd := newTUICmd(opts)

	if cmd.Use != "tui [site] <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}

	err := cmd.RunE(cmd, []string{"unknown-site", "https://example.com"})
	if err == nil {
		t.Error("expected error for unknown site")
	}
}

func TestNewTUICmdAutoDetectsSite(t *testing.T) {
	opts := globalOptions{}
	cmd := newTUICmd(opts)

	// A single arg with an unrecognized domain should hit detectSite's
	// error path rather than being treated as a (missing) site name.
	err := cmd.RunE(cmd, []string{"https://example.com/whatever"})
	if err == nil {
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

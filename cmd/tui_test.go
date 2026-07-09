package cmd

import "testing"

func TestNewTUICmd(t *testing.T) {
	cmd := newTUICmd()

	if cmd.Use != "tui <site> <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("expected --output flag")
	}
	if cmd.Flags().Lookup("concurrent") == nil {
		t.Error("expected --concurrent flag")
	}

	err := cmd.RunE(cmd, []string{"unknown-site", "https://example.com"})
	if err == nil {
		t.Error("expected error for unknown site")
	}
}

func TestScraperRegistry(t *testing.T) {
	reg := scraperRegistry()
	for _, name := range []string{"youtube", "gofile", "cyberdrop", "bunker"} {
		if _, ok := reg[name]; !ok {
			t.Errorf("expected registry to contain %q", name)
		}
	}
}

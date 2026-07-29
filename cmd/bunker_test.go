package cmd

import "testing"

func TestNewBunkerCmd(t *testing.T) {
	opts := globalOptions{}
	cmd := newBunkerCmd(opts)

	if cmd.Use != "bunker <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	if cmd.Flags().Lookup("url") == nil {
		t.Error("expected --url flag")
	}

	if err := cmd.RunE(cmd, []string{}); err == nil {
		t.Error("expected error when no URL is provided")
	}
}

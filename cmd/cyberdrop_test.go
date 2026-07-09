package cmd

import "testing"

func TestNewCyberDropCmd(t *testing.T) {
	cmd := newCyberDropCmd()

	if cmd.Use != "cyberdrop <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("expected --output flag")
	}
	if cmd.Flags().Lookup("concurrent") == nil {
		t.Error("expected --concurrent flag")
	}

	if err := cmd.RunE(cmd, []string{}); err == nil {
		t.Error("expected error when no URL is provided")
	}
}

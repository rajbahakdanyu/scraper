package cmd

import "testing"

func TestNewCyberDropCmd(t *testing.T) {
	opts := globalOptions{}
	cmd := newCyberDropCmd(opts)

	if cmd.Use != "cyberdrop <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}

	if err := cmd.RunE(cmd, []string{}); err == nil {
		t.Error("expected error when no URL is provided")
	}
}

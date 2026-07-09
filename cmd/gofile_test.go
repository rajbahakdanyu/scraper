package cmd

import "testing"

func TestNewGoFileCmd(t *testing.T) {
	opts := globalOptions{}
	cmd := newGoFileCmd(opts)

	if cmd.Use != "gofile <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}

	if err := cmd.RunE(cmd, []string{}); err == nil {
		t.Error("expected error when no URL is provided")
	}
}

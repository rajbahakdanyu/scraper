package cmd

import "testing"

func TestNewFilesterCmd(t *testing.T) {
	opts := globalOptions{}
	cmd := newFilesterCmd(opts)

	if cmd.Use != "filester <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}

	if err := cmd.RunE(cmd, []string{}); err == nil {
		t.Error("expected error when no URL is provided")
	}
}

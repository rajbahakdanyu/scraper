// cmd/gofile_test.go
package cmd

import "testing"

func TestNewGoFileCmd(t *testing.T) {
	cmd := newGoFileCmd()

	if cmd.Use != "gofile <url>" {
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

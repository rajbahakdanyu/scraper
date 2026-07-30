package cmd

import "testing"

func TestNewYouTubeCmd(t *testing.T) {
	cmd := newYouTubeCmd()

	if cmd.Use != "youtube <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	if cmd.Flags().Lookup("url") == nil {
		t.Error("expected --url flag")
	}

	if err := cmd.RunE(cmd, []string{}); err == nil {
		t.Error("expected error when no URL is provided")
	}
}

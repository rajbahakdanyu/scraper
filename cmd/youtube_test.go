// cmd/youtube_test.go
package cmd

import "testing"

func TestNewYouTubeCmd(t *testing.T) {
	cmd := newYouTubeCmd()

	if cmd.Use != "youtube <url>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("expected --output flag")
	}
	if cmd.Flags().Lookup("concurrent") == nil {
		t.Error("expected --concurrent flag")
	}
	if cmd.Flags().Lookup("url") == nil {
		t.Error("expected --url flag")
	}

	if err := cmd.RunE(cmd, []string{}); err == nil {
		t.Error("expected error when no URL is provided")
	}
}

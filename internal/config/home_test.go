package config

import (
	"strings"
	"testing"
)

func TestExpandHomePathRejectsUserHome(t *testing.T) {
	_, err := ExpandHomePath("~other/file")
	if err == nil || !strings.Contains(err.Error(), "~user") {
		t.Fatalf("ExpandHomePath() error = %v, want ~user error", err)
	}
}

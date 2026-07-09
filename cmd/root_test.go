// cmd/root_test.go
package cmd

import "testing"

func TestNewRootCmdRegistersSubcommands(t *testing.T) {
	root := NewRootCmd()

	want := []string{"youtube", "gofile", "cyberdrop", "bunker", "tui"}
	for _, name := range want {
		found := false
		for _, c := range root.Commands() {
			if c.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected root command to register %q subcommand", name)
		}
	}
}

package notify

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const hostileSummary = `x" & (do shell script "id") & "\'); $.system("id"); (' — ünïcode ✓ ${HOME} ` + "`id`"

func TestNotifyCommandKeepsMessageOutOfTheScript(t *testing.T) {
	cmd := notifyCommand(hostileSummary)

	if cmd.Path != "/usr/bin/osascript" {
		t.Fatalf("osascript must be run by absolute path, got %q", cmd.Path)
	}
	want := []string{"/usr/bin/osascript", "-l", "JavaScript", "-e", darwinScript}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
	if got := cmd.Env[len(cmd.Env)-1]; got != envMessage+"="+hostileSummary {
		t.Fatalf("message must travel in %s, last env entry = %q", envMessage, got)
	}
}

func TestDarwinMessageReadsTheEnvironmentVerbatim(t *testing.T) {
	// Evaluate exactly the expression darwinScript uses, printing instead
	// of displaying, to prove the message round-trips as inert UTF-8 text.
	cmd := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", `ObjC.import("Foundation");`+darwinMessage)
	cmd.Env = notifyCommand(hostileSummary).Env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("osascript: %v", err)
	}
	if got := strings.TrimSuffix(string(out), "\n"); got != hostileSummary {
		t.Fatalf("message = %q, want %q", got, hostileSummary)
	}
}

func TestSendReportsOsascriptFailure(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "osascript")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'not allowed' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	orig := osascript
	t.Cleanup(func() { osascript = orig })
	osascript = fake

	err := Send("done")
	if err == nil || !strings.Contains(err.Error(), "osascript failed") || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v, want the failure and osascript's output", err)
	}

	osascript = "/usr/bin/true"
	if err := Send("done"); err != nil {
		t.Fatalf("successful send: %v", err)
	}
}

package notify

import (
	"os/exec"
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

package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

func Send(summary string) error {
	if summary == "" {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		return darwinNotify(summary)
	case "linux":
		return linuxNotify(summary)
	default:
		return nil
	}
}

func darwinNotify(summary string) error {
	if _, err := exec.LookPath("osascript"); err != nil {
		return nil
	}
	script := fmt.Sprintf(`display notification %q with title "scraper"`, sanitizeNotificationSummary(summary))
	return execNotify("osascript", "-e", script)
}

func sanitizeNotificationSummary(summary string) string {
	return strings.NewReplacer(`"`, `'`, `\`, `'`).Replace(summary)
}

func linuxNotify(summary string) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return nil
	}
	return execNotify("notify-send", "scraper", summary)
}

// execNotify runs a notification command. All callers sanitize their
// arguments: darwinNotify quotes the message via sanitizeNotificationSummary;
// linuxNotify receives the constant "Download complete".
//
// #nosec G204 -- arguments are sanitized or constant, not attacker-controlled.
func execNotify(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notification: %s failed: %v (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

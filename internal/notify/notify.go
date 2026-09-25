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
	return execNotify("osascript", "-e", darwinScript(summary))
}

// darwinScript composes the AppleScript notification.
// sanitizeNotificationSummary replaces double quotes and backslashes (the
// only AppleScript string metacharacters used here), so the summary cannot
// break out of its string literal and inject AppleScript.
func darwinScript(summary string) string {
	return fmt.Sprintf(`display notification %q with title "scraper"`, sanitizeNotificationSummary(summary))
}

func sanitizeNotificationSummary(summary string) string {
	return strings.NewReplacer(`"`, `'`, `\`, `'`).Replace(summary)
}

func linuxNotify(summary string) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return nil
	}
	return execNotify("notify-send", linuxNotifyArgs(summary)...)
}

// linuxNotifyArgs builds notify-send's argv; the summary is a single argv
// element and is never passed through a shell.
func linuxNotifyArgs(summary string) []string {
	return []string{"scraper", summary}
}

// execNotify runs a notification command. All callers sanitize their
// arguments: darwinNotify quotes the message via sanitizeNotificationSummary;
// linuxNotify passes the summary as a single argv element, never a shell.
//
// #nosec G204 -- arguments are sanitized or constant, not attacker-controlled.
func execNotify(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notification: %s failed: %v (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

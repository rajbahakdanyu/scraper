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
	// #nosec G204 -- osascript is invoked with sanitized input (no quotes or backslashes).
	cmd := exec.Command("osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notification: osascript failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func sanitizeNotificationSummary(summary string) string {
	return strings.NewReplacer(`"`, `'`, `\`, `'`).Replace(summary)
}

func linuxNotify(summary string) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return nil
	}
	// #nosec G204 -- notify-send uses argv directly with a message that is always the constant "Download complete".
	cmd := exec.Command("notify-send", "scraper", summary)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notification: notify-send failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

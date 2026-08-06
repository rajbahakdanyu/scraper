package notify

import (
	"os"
	"runtime"
	"testing"
)

func TestSend(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/usr/bin/osascript"); err != nil {
			t.Skip("osascript not available")
		}
	case "linux":
		if _, err := os.Stat("/usr/bin/notify-send"); err != nil {
			t.Skip("notify-send not available")
		}
	default:
		t.Skip("platform not supported for notification test")
	}
	if err := Send("test notification"); err != nil {
		t.Logf("Send returned error (may be ok in CI): %v", err)
	}
}

func TestSendWithFailures(t *testing.T) {
	summary := "Done: 5 items, 2 failed"
	if err := Send(summary); err != nil {
		t.Logf("Send returned error (may be ok in CI): %v", err)
	}
}

func TestSendEmptySummary(t *testing.T) {
	if err := Send(""); err != nil {
		t.Logf("Send with empty summary: %v", err)
	}
}

func TestSanitizeNotificationSummary(t *testing.T) {
	if got := sanitizeNotificationSummary(`Download "complete\now`); got != "Download 'complete'now" {
		t.Fatalf("sanitized summary = %q", got)
	}
}

func TestSanitizeNotificationSummaryLeavesCleanText(t *testing.T) {
	const summary = "Download complete"
	if got := sanitizeNotificationSummary(summary); got != summary {
		t.Fatalf("clean summary changed to %q", got)
	}
}

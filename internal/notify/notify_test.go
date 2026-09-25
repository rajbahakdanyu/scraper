package notify

import (
	"strings"
	"testing"
)

func TestDarwinScriptSanitizesSummary(t *testing.T) {
	got := darwinScript(`a"b\c`)
	// %q both quotes the (already sanitized) summary and escapes any
	// remaining special bytes, so the literal is always terminated by the
	// script's own quote characters.
	want := `display notification "a'b'c" with title "scraper"`
	if got != want {
		t.Fatalf("darwinScript() = %q, want %q", got, want)
	}
}

func TestDarwinScriptNeutralizesAppleScriptInjection(t *testing.T) {
	got := darwinScript(`x" & (do shell script "id") & "`)
	want := `display notification "x' & (do shell script 'id') & '" with title "scraper"`
	if got != want {
		t.Fatalf("darwinScript() = %q, want %q", got, want)
	}
	// The summary's double quotes are gone, so it cannot close the
	// notification string literal early; the only remaining quotes are the
	// %q wrapping and the title literal.
	if strings.Count(got, `"`) != 4 {
		t.Fatalf("unexpected quote count in script: %q", got)
	}
}

func TestLinuxNotifyArgsPassSummaryAsSingleArg(t *testing.T) {
	got := linuxNotifyArgs(`hello "world"; rm -rf /`)
	want := []string{"scraper", `hello "world"; rm -rf /`}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("linuxNotifyArgs() = %q, want %q", got, want)
	}
}

func TestSendEmptySummaryIsNoop(t *testing.T) {
	if err := Send(""); err != nil {
		t.Fatalf("Send(\"\") = %v, want nil", err)
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

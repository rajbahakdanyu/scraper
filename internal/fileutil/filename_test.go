package fileutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "dot", input: ".", want: ""},
		{name: "dot dot", input: "..", want: ""},
		{name: "empty", input: "", want: ""},
		{name: "path separators only", input: `/\\`, want: ""},
		{name: "control characters", input: "a\x00\x01\x1f\nb", want: "ab"},
		{name: "invalid filename characters", input: "?*:|<>", want: ""},
		{name: "unicode preserved", input: "café.mp4", want: "café.mp4"},
		{name: "leading hyphen preserved", input: "-start", want: "-start"},
		{name: "path separators stripped", input: "path/to\\file.mp4", want: "pathtofile.mp4"},
		// The budget leaves room for ".part"/".resume.json"/".sha256"
		// suffixes added after sanitization.
		{name: "budget bytes preserved", input: strings.Repeat("x", filenameBudget), want: strings.Repeat("x", filenameBudget)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeFilename(tt.input); got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeFilenameClampsLength(t *testing.T) {
	long := strings.Repeat("a", 300) + ".mp4"
	got := SanitizeFilename(long)
	if len(got) > filenameBudget {
		t.Fatalf("len(%q) = %d, want <= %d", got, len(got), filenameBudget)
	}
	if !strings.HasSuffix(got, ".mp4") {
		t.Fatalf("extension lost: %q", got)
	}
	if SanitizeFilename(long) != got {
		t.Fatal("truncation must be deterministic")
	}
	if SanitizeFilename(strings.Repeat("b", 300)+".mp4") == got {
		t.Fatal("different long names must not collide")
	}
}

func TestSanitizeFilenameClampsUnicodeWithoutSplittingRunes(t *testing.T) {
	long := strings.Repeat("é", 300) + ".bin"
	got := SanitizeFilename(long)
	if len(got) > 255 {
		t.Fatalf("len = %d, want <= 255", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated to invalid UTF-8: %q", got)
	}
}

func TestSanitizeFilenameWindowsReservedNames(t *testing.T) {
	for in, want := range map[string]string{
		"CON":       "_CON",
		"con.txt":   "_con.txt",
		"nul":       "_nul",
		"COM1.mp4":  "_COM1.mp4",
		"LPT9":      "_LPT9",
		"CONSOLE":   "CONSOLE",
		"CONSOLE.1": "CONSOLE.1",
	} {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFilenameStripsTrailingDotsAndSpaces(t *testing.T) {
	for in, want := range map[string]string{
		"file.  ": "file",
		"file...": "file",
		"a . ":    "a",
		"...":     "",
	} {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

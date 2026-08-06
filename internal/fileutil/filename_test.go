package fileutil

import (
	"strings"
	"testing"
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
		{name: "255 characters preserved", input: strings.Repeat("x", 255), want: strings.Repeat("x", 255)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeFilename(tt.input); got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

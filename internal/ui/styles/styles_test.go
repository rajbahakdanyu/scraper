// internal/ui/styles/styles_test.go
package styles

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestDefaultThemeColors(t *testing.T) {
	theme := DefaultTheme()
	for name, tc := range map[string]struct {
		got  lipgloss.TerminalColor
		want string
	}{
		"Accent":    {theme.Accent.GetForeground(), ColorAccent},
		"Success":   {theme.Success.GetForeground(), ColorGreen},
		"Error":     {theme.Error.GetForeground(), ColorRed},
		"Muted":     {theme.Muted.GetForeground(), ColorMuted},
		"Footer":    {theme.Footer.GetForeground(), ColorMuted},
		"StatusBar": {theme.StatusBar.GetBackground(), ColorPanel},
	} {
		if tc.got != lipgloss.Color(tc.want) {
			t.Errorf("%s color = %v, want %s", name, tc.got, tc.want)
		}
	}
	if !theme.Success.GetBold() || !theme.Error.GetBold() {
		t.Error("success and error markers must be bold to stand out")
	}
}

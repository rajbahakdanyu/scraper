// internal/ui/styles/styles_test.go
package styles

import "testing"

func TestDefaultTheme(t *testing.T) {
	theme := DefaultTheme()
	if theme.Accent.GetForeground() == nil {
		t.Error("expected accent color to be set")
	}
}

func TestHighContrastTheme(t *testing.T) {
	theme := HighContrastTheme()
	if theme.Accent.GetForeground() == nil {
		t.Error("expected accent color to be set")
	}
}

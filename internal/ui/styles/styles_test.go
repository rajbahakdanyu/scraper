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

// TestHighContrastThemeDiffersFromDefault guards against HighContrastTheme
// silently regressing back into a plain alias of DefaultTheme: the whole
// point of a high-contrast theme is that dim/muted text becomes bright, so
// at least those styles must actually differ between the two.
func TestHighContrastThemeDiffersFromDefault(t *testing.T) {
	def := DefaultTheme()
	hc := HighContrastTheme()

	if hc.Dim.GetForeground() == def.Dim.GetForeground() {
		t.Error("expected HighContrastTheme.Dim to use a different (brighter) foreground than DefaultTheme.Dim")
	}
	if hc.Muted.GetForeground() == def.Muted.GetForeground() {
		t.Error("expected HighContrastTheme.Muted to use a different (brighter) foreground than DefaultTheme.Muted")
	}
	if hc.Footer.GetForeground() == def.Footer.GetForeground() {
		t.Error("expected HighContrastTheme.Footer to use a different (brighter) foreground than DefaultTheme.Footer")
	}
}

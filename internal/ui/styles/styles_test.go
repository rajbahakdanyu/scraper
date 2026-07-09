package styles
// internal/ui/styles/styles_test.go
package styles

import "testing"

func TestDefaultTheme(t *testing.T) {
	theme := DefaultTheme()

	if !theme.Success.GetBold() {
		t.Error("expected success style to be bold")
	}
	if !theme.Error.GetBold() {
		t.Error("expected error style to be bold")
	}
	if !theme.Header.GetBold() {
		t.Error("expected header style to be bold")
	}
	if theme.Header.GetPaddingLeft() == 0 {
		t.Error("expected header style to have horizontal padding")
	}
	if theme.Primary.Render("x") == "" {
		t.Error("expected primary style to render content")
	}
}

func TestHighContrastTheme(t *testing.T) {
	theme := HighContrastTheme()

	if !theme.Success.GetBold() {
		t.Error("expected success style to be bold")
	}
	if !theme.Warning.GetBold() {
		t.Error("expected warning style to be bold")
	}
	if !theme.Error.GetBold() {
		t.Error("expected error style to be bold")
	}
	if !theme.Header.GetBold() {
		t.Error("expected header style to be bold")
	}
}

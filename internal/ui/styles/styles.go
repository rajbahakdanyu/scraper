// internal/ui/styles/styles.go
package styles

import "github.com/charmbracelet/lipgloss"

// Color palette — subdued on dark
const (
	ColorAccent  = "99"  // muted violet
	ColorCyan    = "37"  // steel blue
	ColorGreen   = "71"  // muted green
	ColorRed     = "131" // muted crimson
	ColorMuted   = "240" // dark gray
	ColorDim     = "244" // medium gray
	ColorText    = "252" // near-white
	ColorPanel   = "236" // dark panel bg
	ColorPanelBd = "60"  // muted purple border
)

// Theme bundles all styles used across the TUI.
type Theme struct {
	Accent    lipgloss.Style
	Cyan      lipgloss.Style
	Success   lipgloss.Style
	Error     lipgloss.Style
	Muted     lipgloss.Style
	Dim       lipgloss.Style
	Bright    lipgloss.Style
	Label     lipgloss.Style
	Value     lipgloss.Style
	Panel     lipgloss.Style
	PanelSlim lipgloss.Style
	StatusBar lipgloss.Style
	Footer    lipgloss.Style
	Title     lipgloss.Style
}

func DefaultTheme() Theme {
	return Theme{
		Accent:  lipgloss.NewStyle().Foreground(lipgloss.Color(ColorAccent)).Bold(true),
		Cyan:    lipgloss.NewStyle().Foreground(lipgloss.Color(ColorCyan)),
		Success: lipgloss.NewStyle().Foreground(lipgloss.Color(ColorGreen)).Bold(true),
		Error:   lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRed)).Bold(true),
		Muted:   lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted)),
		Dim:     lipgloss.NewStyle().Foreground(lipgloss.Color(ColorDim)),
		Bright:  lipgloss.NewStyle().Foreground(lipgloss.Color(ColorText)).Bold(true),
		Label:   lipgloss.NewStyle().Foreground(lipgloss.Color(ColorDim)),
		Value:   lipgloss.NewStyle().Foreground(lipgloss.Color(ColorCyan)).Bold(true),
		Panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ColorPanelBd)).
			Padding(0, 2),
		PanelSlim: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ColorPanelBd)).
			Padding(0, 1),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color(ColorPanel)).
			Foreground(lipgloss.Color(ColorText)).
			Padding(0, 2),
		Footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)),
		Title: lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorCyan)).
			Bold(true),
	}
}

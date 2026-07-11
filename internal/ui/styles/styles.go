// internal/ui/styles/styles.go
package styles

import "github.com/charmbracelet/lipgloss"

// Color palette — cyberpunk neon on dark
const (
	ColorAccent  = "141" // purple
	ColorCyan    = "51"  // bright cyan
	ColorPink    = "213" // hot pink
	ColorGreen   = "84"  // neon green
	ColorRed     = "203" // coral red
	ColorYellow  = "221" // soft yellow
	ColorMuted   = "240" // dark gray
	ColorDim     = "244" // medium gray
	ColorText    = "252" // near-white
	ColorPanel   = "236" // dark panel bg
	ColorPanelBd = "60"  // muted purple border
	ColorBar     = "141" // progress bar
)

// Theme bundles all styles used across the TUI.
type Theme struct {
	Accent    lipgloss.Style
	Cyan      lipgloss.Style
	Pink      lipgloss.Style
	Success   lipgloss.Style
	Error     lipgloss.Style
	Warning   lipgloss.Style
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
		Pink:    lipgloss.NewStyle().Foreground(lipgloss.Color(ColorPink)).Bold(true),
		Success: lipgloss.NewStyle().Foreground(lipgloss.Color(ColorGreen)).Bold(true),
		Error:   lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRed)).Bold(true),
		Warning: lipgloss.NewStyle().Foreground(lipgloss.Color(ColorYellow)),
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

func HighContrastTheme() Theme {
	return DefaultTheme()
}

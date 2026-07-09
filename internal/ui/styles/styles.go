package styles
// internal/ui/styles/styles.go
package styles

import "github.com/charmbracelet/lipgloss"

// Theme bundles the lipgloss styles shared across the TUI components.
type Theme struct {
	Primary   lipgloss.Style
	Secondary lipgloss.Style
	Success   lipgloss.Style
	Warning   lipgloss.Style
	Error     lipgloss.Style
	Header    lipgloss.Style
	Border    lipgloss.Style
}

// DefaultTheme returns the standard 256-color dark theme.
func DefaultTheme() Theme {
	return Theme{
		Primary:   lipgloss.NewStyle().Foreground(lipgloss.Color("205")),
		Secondary: lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		Warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
		Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("87")).Bold(true).Padding(0, 1),
		Border:    lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")),
	}
}

// HighContrastTheme returns a high-contrast theme for limited-color terminals
// or accessibility needs.
func HighContrastTheme() Theme {
	return Theme{
		Primary:   lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("0")),
		Secondary: lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("0")),
		Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("2")).Bold(true),
		Warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("11")).Bold(true),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("1")).Bold(true),
		Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("15")).Bold(true).Padding(0, 1),
		Border:    lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("15")),
	}
}

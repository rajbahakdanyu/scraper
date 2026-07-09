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
	Title     lipgloss.Style
	Subtitle  lipgloss.Style
	Footer    lipgloss.Style
	Highlight lipgloss.Style
	Muted     lipgloss.Style
	Panel     lipgloss.Style
	StatLabel lipgloss.Style
	StatValue lipgloss.Style
	StatusBar lipgloss.Style
}

// DefaultTheme returns the standard 256-color dark theme.
func DefaultTheme() Theme {
	return Theme{
		Primary:   lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true),
		Secondary: lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		Warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
		Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("87")).Bold(true).Padding(0, 1),
		Title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("213")).
			Background(lipgloss.Color("53")).
			Bold(true).
			Padding(0, 2).
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("213")),
		Subtitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("117")).
			Italic(true).
			Padding(0, 1),
		Border: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("99")),
		Footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Padding(0, 1),
		Highlight: lipgloss.NewStyle().
			Foreground(lipgloss.Color("228")).
			Bold(true),
		Muted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")),
		Panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1, 2),
		StatLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")),
		StatValue: lipgloss.NewStyle().
			Foreground(lipgloss.Color("170")).
			Bold(true),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1),
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
		Title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("15")).
			Bold(true).
			Padding(0, 2),
		Subtitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("11")).
			Padding(0, 1),
		Border: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("15")),
		Footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("15")),
		Highlight: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("11")).
			Bold(true),
		Muted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("8")),
		Panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("15")).
			Padding(1, 2),
		StatLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("8")),
		StatValue: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("11")).
			Bold(true),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color("15")).
			Foreground(lipgloss.Color("0")),
	}
}

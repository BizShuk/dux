package tui

import "github.com/charmbracelet/lipgloss"

// Styles holds all lipgloss styles used in the TUI.
// Keeping them in one place makes it easy to switch themes / add accessibility modes.
type Styles struct {
	Title       lipgloss.Style
	Section     lipgloss.Style
	Path        lipgloss.Style
	Size        lipgloss.Style
	Cursor      lipgloss.Style
	Marked      lipgloss.Style
	Duplicate   lipgloss.Style
	Cold        lipgloss.Style
	Warning     lipgloss.Style
	Help        lipgloss.Style
	StatusBar   lipgloss.Style
	Border      lipgloss.Style
	IndicatorOK lipgloss.Style
}

// DefaultStyles returns the default style set.
func DefaultStyles() Styles {
	return Styles{
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12")).
			Padding(0, 1),
		Section: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Bold(true),
		Path: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")),
		Size: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true),
		Cursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("12")),
		Marked: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true),
		Duplicate: lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")),
		Cold: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")),
		Warning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true),
		Help: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("8")),
		Border: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()),
		IndicatorOK: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")),
	}
}
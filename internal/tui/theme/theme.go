package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Accent string
	Live   string
	Warn   string
	Error  string
	Subtle string

	Root       lipgloss.Style
	Pane       lipgloss.Style
	Active     lipgloss.Style
	Title      lipgloss.Style
	SubtleText lipgloss.Style
	Status     lipgloss.Style
	Tab        lipgloss.Style
	ActiveTab  lipgloss.Style
	ErrorBox   lipgloss.Style
	Modal      lipgloss.Style
	Input      lipgloss.Style
	Code       lipgloss.Style
}

func New(noColor bool) Theme {
	t := Theme{
		Accent: "#7D56F4",
		Live:   "#10B981",
		Warn:   "#F59E0B",
		Error:  "#EF4444",
		Subtle: "#6B7280",
	}
	border := lipgloss.RoundedBorder()
	if noColor {
		t.Root = lipgloss.NewStyle()
		t.Pane = lipgloss.NewStyle().Border(border).Padding(0, 1)
		t.Active = t.Pane.Bold(true)
		t.Title = lipgloss.NewStyle().Bold(true)
		t.SubtleText = lipgloss.NewStyle().Faint(true)
		t.Status = lipgloss.NewStyle().Reverse(true)
		t.Tab = lipgloss.NewStyle()
		t.ActiveTab = lipgloss.NewStyle().Bold(true)
		t.ErrorBox = lipgloss.NewStyle().Border(border).Bold(true).Padding(0, 1)
		t.Modal = lipgloss.NewStyle().Border(border).Padding(1, 2)
		t.Input = lipgloss.NewStyle().Border(border).Padding(0, 1)
		t.Code = lipgloss.NewStyle().Border(border).Padding(0, 1)
		return t
	}
	t.Root = lipgloss.NewStyle()
	t.Pane = lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(t.Subtle)).
		Padding(0, 1)
	t.Active = t.Pane.
		BorderForeground(lipgloss.Color(t.Accent)).
		Bold(true)
	t.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Accent)).
		Bold(true)
	t.SubtleText = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Subtle))
	t.Status = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color(t.Accent))
	t.Tab = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Subtle)).
		Padding(0, 1)
	t.ActiveTab = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color(t.Accent)).
		Bold(true).
		Padding(0, 1)
	t.ErrorBox = lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(t.Error)).
		Foreground(lipgloss.Color(t.Error)).
		Padding(0, 1)
	t.Modal = lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(t.Accent)).
		Padding(1, 2)
	t.Input = lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(t.Accent)).
		Padding(0, 1)
	t.Code = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(t.Subtle)).
		Foreground(lipgloss.Color("#D1D5DB")).
		Padding(0, 1)
	return t
}

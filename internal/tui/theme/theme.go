package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Name string

	Accent       string
	Live         string
	Warn         string
	Error        string
	Info         string
	Subtle       string
	Bg           string
	Surface      string
	SurfaceAlt   string
	Fg           string
	FgMuted      string
	Border       string
	BorderActive string

	Root             lipgloss.Style
	Pane             lipgloss.Style
	Active           lipgloss.Style
	Selected         lipgloss.Style
	Title            lipgloss.Style
	Subtitle         lipgloss.Style
	Caption          lipgloss.Style
	SubtleText       lipgloss.Style
	Status           lipgloss.Style
	StatusBrand      lipgloss.Style
	StatusSegment    lipgloss.Style
	StatusSegmentAlt lipgloss.Style
	StatusAccent     lipgloss.Style
	StatusWarn       lipgloss.Style
	StatusError      lipgloss.Style
	StatusSeparator  lipgloss.Style
	Tab              lipgloss.Style
	ActiveTab        lipgloss.Style
	ErrorBox         lipgloss.Style
	Modal            lipgloss.Style
	Input            lipgloss.Style
	Code             lipgloss.Style
}

type palette struct {
	Accent       string
	Live         string
	Warn         string
	Error        string
	Info         string
	Subtle       string
	Bg           string
	Surface      string
	SurfaceAlt   string
	Fg           string
	FgMuted      string
	Border       string
	BorderActive string
	Selected     string
	StatusFg     string
}

var palettes = map[string]palette{
	"catppuccin": {
		Accent:       "#cba6f7", // mauve
		Live:         "#a6e3a1", // green
		Warn:         "#f9e2af", // yellow
		Error:        "#f38ba8", // red
		Info:         "#89b4fa", // blue
		Subtle:       "#6c7086", // overlay0
		Bg:           "#1e1e2e", // base
		Surface:      "#313244", // surface0
		SurfaceAlt:   "#45475a", // surface1
		Fg:           "#cdd6f4", // text
		FgMuted:      "#a6adc8", // subtext0
		Border:       "#585b70", // surface2
		BorderActive: "#cba6f7", // mauve
		Selected:     "#45475a",
		StatusFg:     "#1e1e2e",
	},
	"tokyonight": {
		Accent:       "#bb9af7", // purple
		Live:         "#9ece6a", // green
		Warn:         "#e0af68", // yellow
		Error:        "#f7768e", // red
		Info:         "#7aa2f7", // blue
		Subtle:       "#565f89",
		Bg:           "#1a1b26",
		Surface:      "#24283b",
		SurfaceAlt:   "#414868",
		Fg:           "#c0caf5",
		FgMuted:      "#a9b1d6",
		Border:       "#414868",
		BorderActive: "#bb9af7",
		Selected:     "#283457",
		StatusFg:     "#1a1b26",
	},
	"gruvbox": {
		Accent:       "#d3869b", // pink
		Live:         "#b8bb26", // green
		Warn:         "#fabd2f", // yellow
		Error:        "#fb4934", // red
		Info:         "#83a598", // blue
		Subtle:       "#928374",
		Bg:           "#282828",
		Surface:      "#3c3836",
		SurfaceAlt:   "#504945",
		Fg:           "#ebdbb2",
		FgMuted:      "#d5c4a1",
		Border:       "#504945",
		BorderActive: "#d3869b",
		Selected:     "#504945",
		StatusFg:     "#282828",
	},
	"rosepine": {
		Accent:       "#c4a7e7", // iris
		Live:         "#9ccfd8", // foam
		Warn:         "#f6c177", // gold
		Error:        "#eb6f92", // love
		Info:         "#31748f", // pine
		Subtle:       "#6e6a86",
		Bg:           "#191724",
		Surface:      "#1f1d2e",
		SurfaceAlt:   "#26233a",
		Fg:           "#e0def4",
		FgMuted:      "#908caa",
		Border:       "#403d52",
		BorderActive: "#c4a7e7",
		Selected:     "#26233a",
		StatusFg:     "#191724",
	},
	"onedark": {
		Accent:       "#c678dd", // purple
		Live:         "#98c379", // green
		Warn:         "#e5c07b", // yellow
		Error:        "#e06c75", // red
		Info:         "#61afef", // blue
		Subtle:       "#5c6370", // comment
		Bg:           "#282c34",
		Surface:      "#2c323c", // cursor line
		SurfaceAlt:   "#3e4452", // selection
		Fg:           "#abb2bf",
		FgMuted:      "#828997",
		Border:       "#3e4452",
		BorderActive: "#c678dd",
		Selected:     "#3e4452",
		StatusFg:     "#282c34",
	},
}

func pickPalette(name string) (string, palette) {
	if p, ok := palettes[name]; ok {
		return name, p
	}
	return "catppuccin", palettes["catppuccin"]
}

func New(name string, noColor bool) Theme {
	resolvedName, p := pickPalette(name)

	t := Theme{
		Name:         resolvedName,
		Accent:       p.Accent,
		Live:         p.Live,
		Warn:         p.Warn,
		Error:        p.Error,
		Info:         p.Info,
		Subtle:       p.Subtle,
		Bg:           p.Bg,
		Surface:      p.Surface,
		SurfaceAlt:   p.SurfaceAlt,
		Fg:           p.Fg,
		FgMuted:      p.FgMuted,
		Border:       p.Border,
		BorderActive: p.BorderActive,
	}

	rounded := lipgloss.RoundedBorder()
	thick := lipgloss.ThickBorder()
	normal := lipgloss.NormalBorder()

	if noColor {
		t.Root = lipgloss.NewStyle()
		t.Pane = lipgloss.NewStyle().Border(rounded).Padding(0, 2, 0, 1)
		t.Active = lipgloss.NewStyle().Border(thick).Padding(0, 2, 0, 1).Bold(true)
		t.Selected = lipgloss.NewStyle().Reverse(true)
		t.Title = lipgloss.NewStyle().Bold(true)
		t.Subtitle = lipgloss.NewStyle().Bold(true).Faint(true)
		t.Caption = lipgloss.NewStyle().Italic(true).Faint(true)
		t.SubtleText = lipgloss.NewStyle().Faint(true)
		t.Status = lipgloss.NewStyle().Reverse(true)
		t.StatusBrand = lipgloss.NewStyle().Reverse(true).Bold(true).Padding(0, 1)
		t.StatusSegment = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
		t.StatusSegmentAlt = lipgloss.NewStyle().Reverse(true).Faint(true).Padding(0, 1)
		t.StatusAccent = lipgloss.NewStyle().Reverse(true).Bold(true).Padding(0, 1)
		t.StatusWarn = lipgloss.NewStyle().Reverse(true).Bold(true).Padding(0, 1)
		t.StatusError = lipgloss.NewStyle().Reverse(true).Bold(true).Padding(0, 1)
		t.StatusSeparator = lipgloss.NewStyle()
		t.Tab = lipgloss.NewStyle().Padding(0, 1)
		t.ActiveTab = lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)
		t.ErrorBox = lipgloss.NewStyle().Border(rounded).Bold(true).Padding(0, 2)
		t.Modal = lipgloss.NewStyle().Border(thick).Padding(1, 3)
		t.Input = lipgloss.NewStyle().Border(rounded).Padding(0, 2)
		t.Code = lipgloss.NewStyle().Border(normal).Padding(0, 1)
		return t
	}

	border := lipgloss.Color(t.Border)
	borderActive := lipgloss.Color(t.BorderActive)
	accent := lipgloss.Color(t.Accent)
	fg := lipgloss.Color(t.Fg)
	fgMuted := lipgloss.Color(t.FgMuted)
	subtle := lipgloss.Color(t.Subtle)
	surface := lipgloss.Color(t.Surface)
	surfaceAlt := lipgloss.Color(t.SurfaceAlt)
	selectedBg := lipgloss.Color(p.Selected)
	statusFg := lipgloss.Color(p.StatusFg)

	t.Root = lipgloss.NewStyle().Foreground(fg)

	t.Pane = lipgloss.NewStyle().
		Border(rounded).
		BorderForeground(border).
		Padding(0, 2, 0, 1)

	t.Active = lipgloss.NewStyle().
		Border(thick).
		BorderForeground(borderActive).
		Foreground(fg).
		Padding(0, 2, 0, 1).
		Bold(true)

	t.Selected = lipgloss.NewStyle().
		Foreground(fg).
		Background(selectedBg).
		Bold(true)

	t.Title = lipgloss.NewStyle().
		Foreground(accent).
		Bold(true)

	t.Subtitle = lipgloss.NewStyle().
		Foreground(fgMuted).
		Bold(true)

	t.Caption = lipgloss.NewStyle().
		Foreground(subtle).
		Italic(true)

	t.SubtleText = lipgloss.NewStyle().
		Foreground(subtle)

	t.Status = lipgloss.NewStyle().
		Foreground(fg).
		Background(surface)

	t.StatusBrand = lipgloss.NewStyle().
		Foreground(statusFg).
		Background(accent).
		Bold(true).
		Padding(0, 1)

	t.StatusSegment = lipgloss.NewStyle().
		Foreground(fg).
		Background(surface).
		Padding(0, 1)

	t.StatusSegmentAlt = lipgloss.NewStyle().
		Foreground(fgMuted).
		Background(surfaceAlt).
		Padding(0, 1)

	t.StatusAccent = lipgloss.NewStyle().
		Foreground(statusFg).
		Background(lipgloss.Color(t.Live)).
		Bold(true).
		Padding(0, 1)

	t.StatusWarn = lipgloss.NewStyle().
		Foreground(statusFg).
		Background(lipgloss.Color(t.Warn)).
		Bold(true).
		Padding(0, 1)

	t.StatusError = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color(t.Error)).
		Bold(true).
		Padding(0, 1)

	t.StatusSeparator = lipgloss.NewStyle().
		Foreground(subtle).
		Background(surface)

	t.Tab = lipgloss.NewStyle().
		Foreground(fgMuted).
		Background(surface).
		Padding(0, 2)

	t.ActiveTab = lipgloss.NewStyle().
		Foreground(statusFg).
		Background(accent).
		Bold(true).
		Padding(0, 2)

	t.ErrorBox = lipgloss.NewStyle().
		Border(rounded).
		BorderForeground(lipgloss.Color(t.Error)).
		Foreground(lipgloss.Color(t.Error)).
		Bold(true).
		Padding(0, 2)

	t.Modal = lipgloss.NewStyle().
		Border(thick).
		BorderForeground(borderActive).
		Foreground(fg).
		Padding(1, 3)

	t.Input = lipgloss.NewStyle().
		Border(rounded).
		BorderForeground(border).
		Foreground(fg).
		Padding(0, 2)

	t.Code = lipgloss.NewStyle().
		Border(normal).
		BorderForeground(border).
		Foreground(fgMuted).
		Background(surface).
		Padding(0, 1)

	return t
}

func AvailableNames() []string {
	return []string{"catppuccin", "tokyonight", "gruvbox", "rosepine", "onedark"}
}

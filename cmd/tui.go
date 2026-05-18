package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
	"github.com/fabhiantomaoludyo/gws-tui/internal/tui"
)

func runTUI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gws tui", flag.ContinueOnError)
	flags.SetOutput(stderr)

	feature := flags.String("feature", "", "initial feature: chat, mail, calendar, or meet")
	auth := flags.Bool("auth", false, "show the auth screen before loading the workspace")
	noIcons := flags.Bool("no-icons", false, "disable Nerd Font/Unicode icons")
	noColor := flags.Bool("no-color", false, "disable color styling")
	noImages := flags.Bool("no-images", false, "disable inline image previews")
	noVim := flags.Bool("no-vim", false, "disable vim mode keybindings in the composer")
	version := flags.Bool("version", false, "print TUI build version")
	fixtures := flags.Bool("fixtures", false, "force fixture data instead of the installed gws CLI")

	if err := flags.Parse(args); err != nil {
		return 3
	}

	if *version {
		fmt.Fprintf(stdout, "gws tui %s (%s, %s)\n", Version, Commit, Date)
		return 0
	}

	cfg, err := tui.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "gws tui: config: %v\n", err)
		return 3
	}
	if *feature != "" {
		cfg.InitialFeature = *feature
	}
	if *noIcons {
		cfg.NoIcons = true
	}
	if *noColor {
		cfg.NoColor = true
	}
	if *noImages {
		cfg.InlineImages = false
	}
	if *noVim {
		cfg.VimMode = false
	}
	if err := tui.SetupLogging(cfg.LogPath); err != nil {
		fmt.Fprintf(stderr, "gws tui: logging: %v\n", err)
		return 3
	}

	upstream, _ := findUpstreamGWS()
	forceFixtures := *fixtures || shouldUseFixtures() || upstream == ""
	client := api.NewDefaultClient(api.ClientOptions{
		UpstreamPath:  upstream,
		ForceFixture:  forceFixtures,
		FixtureReason: upstreamDescription(),
	})
	defer client.Close()

	model := tui.New(tui.Options{
		Client:       client,
		Config:       cfg,
		ForceAuth:    *auth,
		Version:      Version,
		Commit:       Commit,
		BuildDate:    Date,
		UpstreamHint: upstreamDescription(),
	})

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(context.Background()))
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "gws tui: %v\n", err)
		return 5
	}
	return 0
}

func init() {
	_ = os.Stdin
}

package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/fabhiansan/gws-tui/internal/tui"
)

func runTUI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gws tui", flag.ContinueOnError)
	flags.SetOutput(stderr)

	feature := flags.String("feature", "", "initial feature: chat, mail, calendar, meet, tasks, drive, or docs")
	auth := flags.Bool("auth", false, "show the auth screen before loading the workspace")
	noIcons := flags.Bool("no-icons", false, "disable Nerd Font/Unicode icons")
	noColor := flags.Bool("no-color", false, "disable color styling")
	noImages := flags.Bool("no-images", false, "disable inline image previews")
	noVim := flags.Bool("no-vim", false, "disable vim mode keybindings in the composer")
	daemonMode := flags.Bool("daemon", false, "connect to the background daemon")
	noDaemon := flags.Bool("no-daemon", false, "force standalone single-process mode")
	version := flags.Bool("version", false, "print TUI build version")

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
	if *daemonMode {
		cfg.Daemon = true
	}
	if *noDaemon {
		cfg.Daemon = false
	}
	if err := tui.SetupLogging(cfg.LogPath); err != nil {
		fmt.Fprintf(stderr, "gws tui: logging: %v\n", err)
		return 3
	}

	var snapshot *api.WorkspaceSnapshot
	var client api.WorkspaceClient
	upstreamHint := upstreamDescription()
	if cfg.Daemon {
		remote, daemonSnapshot, err := connectDaemonClient(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "gws tui: daemon: %v\n", err)
			return 5
		}
		client = remote
		snapshot = daemonSnapshot
		upstreamHint = "daemon " + cfg.DaemonSocket
	} else {
		upstream, err := findUpstreamGWS()
		if err != nil || upstream == "" {
			fmt.Fprintf(stderr, "gws tui: upstream Google Workspace CLI not found; install it as `gws` or set GWS_TUI_UPSTREAM\n")
			return 127
		}
		client = api.NewDefaultClient(api.ClientOptions{
			UpstreamPath: upstream,
		})
		// Standalone mode talks to the upstream CLI directly. Resolve the
		// chat delivery strategy in the background so opening a space later
		// does not block on the project lookup + viability probe.
		if configurer, ok := client.(api.ChatEventConfigurer); ok {
			configurer.ConfigureChatEvents(api.ChatEventOptions{
				Disabled:     !cfg.ChatEvents,
				Project:      cfg.ChatEventsProject,
				Subscription: cfg.ChatEventsSubscription,
			})
			go configurer.PrepareChatEvents()
		}
	}
	defer client.Close()

	model := tui.New(tui.Options{
		Client:          client,
		Config:          cfg,
		InitialSnapshot: snapshot,
		ForceAuth:       *auth,
		Version:         Version,
		Commit:          Commit,
		BuildDate:       Date,
		UpstreamHint:    upstreamHint,
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

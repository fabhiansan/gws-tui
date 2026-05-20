package cmd

import (
	"fmt"
	"io"
)

// Build metadata. Release builds override these with -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func Execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printUsage(stdout)
		return 0
	}

	if args[0] == "tui" {
		return runTUI(args[1:], stdout, stderr)
	}
	if args[0] == "daemon" {
		return runDaemon(args[1:], stdout, stderr)
	}

	upstream, err := findUpstreamGWS()
	if err == nil && upstream != "" {
		return delegate(upstream, args, stdout, stderr)
	}

	fmt.Fprintf(stderr, "gws: upstream Google Workspace CLI not found; install it as `gws` or set GWS_TUI_UPSTREAM\n")
	return 127
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `gws — Google Workspace CLI + terminal UI

USAGE:
    gws tui [--feature chat|mail|calendar|meet|tasks|drive|docs] [--daemon|--no-daemon] [--auth] [--no-icons] [--no-color] [--no-images]
    gws daemon start [--detach]
    gws daemon stop|status|logs|restart
    gws <existing gws command> ...

The TUI is implemented locally. Non-TUI commands are delegated to an installed
Google Workspace CLI when available, preserving existing JSON output for the
Neovim Lua plugin.
`)
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

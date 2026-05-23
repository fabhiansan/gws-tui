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
	if len(args) == 0 {
		return runTUI(nil, stdout, stderr)
	}
	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "-v", "--version":
		fmt.Fprintf(stdout, "gws-tui %s (%s, %s)\n", Version, Commit, Date)
		return 0
	case "tui":
		return runTUI(args[1:], stdout, stderr)
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	}
	// Flag-style first arg: treat the whole argv as TUI flags so `gws-tui
	// --feature mail` works without the `tui` subcommand.
	if args[0][0] == '-' {
		return runTUI(args, stdout, stderr)
	}
	fmt.Fprintf(stderr, "gws-tui: unknown command %q\n", args[0])
	printUsage(stderr)
	return 2
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `gws-tui — terminal UI for Google Workspace

USAGE:
    gws-tui [--feature chat|mail|calendar|meet|tasks|drive|docs] [--daemon|--no-daemon]
            [--auth] [--no-icons] [--no-color] [--no-images] [--no-vim]
    gws-tui --version
    gws-tui daemon start [--detach]
    gws-tui daemon stop|status|logs|restart

The TUI reads live data through an authenticated upstream Google Workspace CLI
(installed separately as `+"`gws`"+`). Install it via:
    brew install googleworkspace-cli
or  npm install -g @googleworkspace/cli

Set GWS_TUI_UPSTREAM to point at the upstream binary if it is not on PATH.
`)
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

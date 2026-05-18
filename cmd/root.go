package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
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

	if shouldUseFixtures() {
		return runFixtureCommand(context.Background(), args, stdout, stderr)
	}

	upstream, err := findUpstreamGWS()
	if err == nil && upstream != "" {
		return delegate(upstream, args, stdout, stderr)
	}

	return runFixtureCommand(context.Background(), args, stdout, stderr)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `gws — Google Workspace CLI + terminal UI

USAGE:
    gws tui [--feature chat|mail|calendar|meet] [--daemon|--no-daemon] [--auth] [--no-icons] [--no-color] [--no-images]
    gws daemon start [--detach]
    gws daemon stop|status|logs|restart
    gws <existing gws command> ...

The TUI is implemented locally. Non-TUI commands are delegated to an installed
Google Workspace CLI when available, preserving existing JSON output for the
Neovim Lua plugin. Set GWS_TUI_USE_FIXTURES=1 for deterministic fixture output.
`)
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func shouldUseFixtures() bool {
	value := strings.ToLower(os.Getenv("GWS_TUI_USE_FIXTURES"))
	return value == "1" || value == "true" || value == "yes"
}

func runFixtureCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	client := api.NewFixtureClient()
	defer client.Close()

	write := func(v any) int {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintf(stderr, "gws: encode response: %v\n", err)
			return 5
		}
		return 0
	}

	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		status, err := client.AuthStatus(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "gws: auth status: %v\n", err)
			return 2
		}
		return write(status)
	}

	if len(args) >= 3 && args[0] == "chat" && args[1] == "spaces" && args[2] == "list" {
		spaces, err := client.ChatSpaces(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "gws: chat spaces list: %v\n", err)
			return 1
		}
		return write(map[string]any{"spaces": spaces.Items, "nextPageToken": spaces.NextPageToken})
	}

	if len(args) >= 4 && args[0] == "chat" && args[1] == "spaces" && args[2] == "messages" && args[3] == "list" {
		spaceName := firstParam(args, "parent", "spaces/engineering")
		messages, err := client.ChatMessages(ctx, spaceName, "")
		if err != nil {
			fmt.Fprintf(stderr, "gws: chat spaces messages list: %v\n", err)
			return 1
		}
		return write(map[string]any{"messages": messages.Items, "nextPageToken": messages.NextPageToken})
	}

	if len(args) >= 4 && args[0] == "gmail" && args[1] == "users" && args[2] == "messages" && args[3] == "list" {
		threads, err := client.MailThreads(ctx, api.MailQuery{Label: "Inbox"})
		if err != nil {
			fmt.Fprintf(stderr, "gws: gmail users messages list: %v\n", err)
			return 1
		}
		messages := make([]map[string]any, 0, len(threads.Items))
		for _, thread := range threads.Items {
			messages = append(messages, map[string]any{
				"id":       thread.ID,
				"threadId": thread.ID,
				"snippet":  thread.Snippet,
				"labelIds": thread.Labels,
			})
		}
		return write(map[string]any{"messages": messages, "nextPageToken": threads.NextPageToken})
	}

	if len(args) >= 3 && args[0] == "calendar" && args[1] == "events" && args[2] == "list" {
		events, err := client.CalendarEvents(ctx, api.CalendarQuery{})
		if err != nil {
			fmt.Fprintf(stderr, "gws: calendar events list: %v\n", err)
			return 1
		}
		return write(map[string]any{"items": events.Items, "nextPageToken": events.NextPageToken})
	}

	if len(args) >= 3 && args[0] == "meet" && args[1] == "spaces" && args[2] == "list" {
		spaces, err := client.MeetSpaces(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "gws: meet spaces list: %v\n", err)
			return 1
		}
		return write(map[string]any{"spaces": spaces.Items, "nextPageToken": spaces.NextPageToken})
	}

	if len(args) >= 3 && args[0] == "meet" && args[1] == "spaces" && args[2] == "create" {
		space, err := client.CreateMeetSpace(ctx, "new-meet-space")
		if err != nil {
			fmt.Fprintf(stderr, "gws: meet spaces create: %v\n", err)
			return 1
		}
		return write(space)
	}

	fmt.Fprintf(stderr, "gws: unsupported fixture command: %s\n", strings.Join(args, " "))
	return 3
}

func firstParam(args []string, name, fallback string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--params" && i+1 < len(args) {
			var params map[string]any
			if json.Unmarshal([]byte(args[i+1]), &params) == nil {
				if v, ok := params[name].(string); ok && v != "" {
					return v
				}
			}
		}
	}
	return fallback
}

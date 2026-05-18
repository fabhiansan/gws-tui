package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureCommandsMatchGolden(t *testing.T) {
	t.Setenv("GWS_TUI_USE_FIXTURES", "1")
	cases := []struct {
		name   string
		args   []string
		golden string
	}{
		{"auth status", []string{"auth", "status"}, "auth_status.json"},
		{"chat spaces", []string{"chat", "spaces", "list"}, "chat_spaces.json"},
		{"chat messages", []string{"chat", "spaces", "messages", "list", "--params", `{"parent":"spaces/engineering"}`}, "chat_messages.json"},
		{"gmail messages", []string{"gmail", "users", "messages", "list"}, "gmail_messages.json"},
		{"calendar events", []string{"calendar", "events", "list"}, "calendar_events.json"},
		{"meet spaces", []string{"meet", "spaces", "list"}, "meet_spaces.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(tc.args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit code %d stderr=%s", code, stderr.String())
			}
			golden, err := os.ReadFile(filepath.Join("..", "testdata", "cli_golden", tc.golden))
			if err != nil {
				t.Fatal(err)
			}
			if stdout.String() != string(golden) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", tc.golden, string(golden), stdout.String())
			}
		})
	}
}

func TestTUIVersionDoesNotStartProgram(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"tui", "--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%s", code, stderr.String())
	}
	if stdout.String() == "" {
		t.Fatal("expected version output")
	}
}

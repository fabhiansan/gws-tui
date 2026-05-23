package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestBareInvocationShowsTUIUsageOnHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "gws-tui") {
		t.Fatalf("expected usage to mention gws-tui; got: %q", stdout.String())
	}
}

func TestTopLevelVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "gws-tui ") {
		t.Fatalf("expected version line, got: %q", stdout.String())
	}
}

func TestUnknownCommandExitsNonZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"auth", "status"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown command; stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown-command message in stderr, got: %q", stderr.String())
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

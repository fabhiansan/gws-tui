package cmd

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("GWS_FAKE_ROOT_COMMAND") == "1" {
		fmt.Printf("delegated:%v\n", os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDelegatesUnknownCommandsToUpstreamGWS(t *testing.T) {
	t.Setenv("GWS_TUI_UPSTREAM", os.Args[0])
	t.Setenv("GWS_FAKE_ROOT_COMMAND", "1")

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"auth", "status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%s", code, stderr.String())
	}
	if got := stdout.String(); got != "delegated:[auth status]\n" {
		t.Fatalf("unexpected delegated output: %q", got)
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

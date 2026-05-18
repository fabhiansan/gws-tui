package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func findUpstreamGWS() (string, error) {
	if explicit := os.Getenv("GWS_TUI_UPSTREAM"); explicit != "" {
		return explicit, nil
	}

	self, _ := os.Executable()
	if self != "" {
		self, _ = filepath.EvalSymlinks(self)
	}

	paths := filepath.SplitList(os.Getenv("PATH"))
	if runtime.GOOS == "darwin" {
		paths = append([]string{"/opt/homebrew/bin", "/usr/local/bin"}, paths...)
	}

	seen := map[string]bool{}
	for _, dir := range paths {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		candidate := filepath.Join(dir, "gws")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			continue
		}
		resolved, _ := filepath.EvalSymlinks(candidate)
		if self != "" && resolved == self {
			continue
		}
		return candidate, nil
	}
	return "", errors.New("upstream gws not found")
}

func delegate(upstream string, args []string, stdout, stderr io.Writer) int {
	command := exec.Command(upstream, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "gws: could not delegate to %s: %v\n", upstream, err)
		return 5
	}
	return 0
}

func upstreamDescription() string {
	upstream, err := findUpstreamGWS()
	if err != nil {
		return "fixture mode"
	}
	return strings.TrimSpace(upstream)
}

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// findUpstreamGWS locates the upstream Google Workspace CLI binary (still
// named `gws`). The TUI and daemon shell out to it for live Workspace data.
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

func upstreamDescription() string {
	upstream, err := findUpstreamGWS()
	if err != nil {
		return "upstream gws not found"
	}
	return strings.TrimSpace(upstream)
}

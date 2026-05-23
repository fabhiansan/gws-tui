package tui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// SetupLogging points the global slog logger at path. The handler is
// deliberately verbose: every record carries a timestamp, a level, and the
// source file and line it was emitted from, so daemon.log and tui.log read as
// a real diagnostic trail rather than bare one-liners. The level defaults to
// INFO and can be lowered to DEBUG (or raised) with GWS_TUI_LOG_LEVEL. When the
// log file cannot be opened, logging falls back to a temp file and, if that
// also fails, is discarded so a missing log never breaks the daemon or the TUI.
func SetupLogging(path string) error {
	opts := &slog.HandlerOptions{
		Level:       logLevelFromEnv(),
		AddSource:   true,
		ReplaceAttr: shortenSourceAttr,
	}
	if path == "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, opts)))
		return nil
	}
	file, err := openLog(path)
	if err != nil {
		file, err = openLog(filepath.Join(os.TempDir(), "gws-tui.log"))
	}
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, opts)))
		return nil
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(file, opts)))
	return nil
}

// logLevelFromEnv reads the minimum log level from GWS_TUI_LOG_LEVEL. An unset
// or unrecognized value keeps the INFO default; "debug" makes the logs as
// verbose as they get.
func logLevelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GWS_TUI_LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// shortenSourceAttr trims the source attribute to "pkg/file.go:line" so log
// lines stay readable without losing the call site that emitted them.
func shortenSourceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key != slog.SourceKey {
		return a
	}
	src, ok := a.Value.Any().(*slog.Source)
	if !ok || src == nil {
		return a
	}
	src.File = trimSourcePath(src.File)
	return a
}

func trimSourcePath(file string) string {
	dir, base := filepath.Split(file)
	parent := filepath.Base(filepath.Clean(dir))
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return base
	}
	return parent + "/" + base
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return file, nil
}

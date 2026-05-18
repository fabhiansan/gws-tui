package tui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

func SetupLogging(path string) error {
	if path == "" {
		return nil
	}
	file, err := openLog(path)
	if err != nil {
		file, err = openLog(filepath.Join(os.TempDir(), "gws-tui.log"))
	}
	if err != nil {
		slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		return nil
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return nil
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

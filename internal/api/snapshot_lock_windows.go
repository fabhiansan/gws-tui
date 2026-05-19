//go:build windows

package api

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockWorkspaceSnapshot(path string, nonblock bool) (*SnapshotLock, error) {
	if path == "" {
		return nil, nil
	}
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonblock {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, ^uint32(0), ^uint32(0), &windows.Overlapped{}); err != nil {
		_ = file.Close()
		if nonblock && errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrSnapshotLockBusy
		}
		return nil, err
	}
	return &SnapshotLock{file: file, path: lockPath}, nil
}

func (l *SnapshotLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
	return l.file.Close()
}

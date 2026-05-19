//go:build !windows

package api

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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
	flag := syscall.LOCK_EX
	if nonblock {
		flag |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), flag); err != nil {
		_ = file.Close()
		if nonblock && errors.Is(err, syscall.EWOULDBLOCK) {
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
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

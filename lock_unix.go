//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package batchlease

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type fileLock interface {
	release()
}

type unixLock struct {
	f *os.File
}

func acquireDirLock(dir string) (fileLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("batchlease: open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("batchlease: lease directory already in use: %w", err)
	}
	return &unixLock{f: f}, nil
}

func (l *unixLock) release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}

// syncDir fsyncs a directory so that a just-renamed entry is durable.
func syncDir(dir string, hook FaultHook) error {
	if hook != nil {
		if err := hook(OpDirSync); err != nil {
			return fmt.Errorf("%w: op %s: %v", ErrInjected, OpDirSync, err)
		}
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

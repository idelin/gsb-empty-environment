//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package batchlease

import "os"

type fileLock interface {
	release()
}

type noopLock struct{}

func acquireDirLock(string) (fileLock, error) { return noopLock{}, nil }

func (noopLock) release() {}

func syncDir(dir string, hook FaultHook) error {
	if hook != nil {
		if err := hook(OpDirSync); err != nil {
			return err
		}
	}
	// Directory fsync is not portable; the rename itself is atomic.
	_, err := os.Stat(dir)
	return err
}

//go:build darwin || linux

package receiver

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// processLocks serializes lock acquisition per state directory within a single
// process. flock alone would deadlock when two Receivers in the same process
// open the same directory (flock is not re-entrant), while flock still provides
// the cross-process guarantee. The map is keyed by the absolute directory path.
var processLocks = newLockRegistry()

type lockRegistry struct {
	mu   chan struct{}
	dirs map[string]chan struct{}
}

func newLockRegistry() *lockRegistry {
	return &lockRegistry{mu: make(chan struct{}, 1), dirs: map[string]chan struct{}{}}
}

func (g *lockRegistry) get(dir string) chan struct{} {
	g.mu <- struct{}{}
	defer func() { <-g.mu }()
	key, err := filepath.Abs(dir)
	if err != nil {
		key = dir
	}
	l, ok := g.dirs[key]
	if !ok {
		l = make(chan struct{}, 1)
		g.dirs[key] = l
	}
	return l
}

// acquireLock takes an exclusive, advisory flock on a dedicated lock file. The
// lock is held for the lifetime of the Receiver and released on Close (or
// automatically by the kernel if the process dies). Combined with the
// in-process mutex this serializes both multiple download threads in one
// process and multiple processes sharing a state directory.
func (r *Receiver) acquireLock() error {
	// Same-process gate first, so reopening a directory already open in this
	// process blocks cooperatively instead of self-deadlocking on flock.
	r.gate = processLocks.get(r.dir)
	r.gate <- struct{}{}

	f, err := os.OpenFile(lockPath(r.dir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		<-r.gate
		return fmt.Errorf("receiver: open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		<-r.gate
		f.Close()
		return fmt.Errorf("receiver: acquire exclusive lock: %w", err)
	}
	r.lockFile = f
	return nil
}

// releaseLock drops the flock and closes the lock file.
func (r *Receiver) releaseLock() error {
	if r.lockFile == nil {
		if r.gate != nil {
			<-r.gate
			r.gate = nil
		}
		return nil
	}
	errUnlock := syscall.Flock(int(r.lockFile.Fd()), syscall.LOCK_UN)
	errClose := r.lockFile.Close()
	r.lockFile = nil
	if r.gate != nil {
		<-r.gate
		r.gate = nil
	}
	if errUnlock != nil {
		return fmt.Errorf("receiver: release lock: %w", errUnlock)
	}
	return errClose
}

// hasLock reports whether this receiver currently owns the directory lock.
func (r *Receiver) hasLock() bool { return r.lockFile != nil || r.gate != nil }

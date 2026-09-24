package hostlock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// ErrStaleToken is returned when a fencing token is not greater than the
// highest token already seen, i.e. it comes from a superseded lock holder.
var ErrStaleToken = errors.New("hostlock: stale fencing token")

// Checker is an in-memory fencing-token gate, modeling the downstream
// resource a lock protects (a file, a queue, a database row): it accepts a
// commit only if its token exceeds every token seen before. A revived old
// holder presenting its old token is rejected.
type Checker struct {
	mu   sync.Mutex
	last uint64
}

// Check accepts token and records it, or fails with ErrStaleToken.
func (c *Checker) Check(token uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if token <= c.last {
		return fmt.Errorf("%w: token %d, already fenced at %d", ErrStaleToken, token, c.last)
	}
	c.last = token
	return nil
}

// Last returns the highest token accepted so far.
func (c *Checker) Last() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// FileFence is a cross-process fencing gate persisted in a file, so the
// fencing decision survives process restarts on the same host.
type FileFence struct {
	path string
}

// NewFileFence returns a FileFence storing its state at path.
func NewFileFence(path string) *FileFence { return &FileFence{path: path} }

// Check accepts token only if it exceeds every token recorded before,
// persisting the new high-water mark (fsync) before returning.
func (f *FileFence) Check(token uint64) error {
	file, err := os.OpenFile(f.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	var last uint64
	data, err := os.ReadFile(f.path)
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &last); err != nil {
			return fmt.Errorf("hostlock: corrupt fence file: %w", err)
		}
	}
	if token <= last {
		return fmt.Errorf("%w: token %d, already fenced at %d", ErrStaleToken, token, last)
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.WriteAt([]byte(strconv.FormatUint(token, 10)+"\n"), 0); err != nil {
		return err
	}
	return file.Sync()
}

// Last returns the persisted high-water mark (0 if none).
func (f *FileFence) Last() (uint64, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var last uint64
	if len(strings.TrimSpace(string(data))) > 0 {
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &last); err != nil {
			return 0, fmt.Errorf("hostlock: corrupt fence file: %w", err)
		}
	}
	return last, nil
}

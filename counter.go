package netsec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// CounterStore persists the monotonic anti-replay counter.
type CounterStore interface {
	// Current returns the most recently committed counter, or zero when no
	// counter has ever been committed.
	Current(ctx context.Context) (uint64, error)
	// Commit stores next when it is strictly greater than the current value.
	// A replayed or stale value is rejected with ErrCounterReplay.
	Commit(ctx context.Context, next uint64) error
}

// MemoryCounter is an in-process CounterStore. It provides no durability across
// restarts and is intended for tests and non-persistent deployments.
type MemoryCounter struct {
	mu      sync.Mutex
	current uint64
}

// NewMemoryCounter returns a MemoryCounter initialised at zero.
func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{}
}

// Current returns the committed counter.
func (m *MemoryCounter) Current(context.Context) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current, nil
}

// Commit advances the counter when next is greater than the current value.
func (m *MemoryCounter) Commit(_ context.Context, next uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if next <= m.current {
		return ErrCounterReplay
	}
	m.current = next
	return nil
}

// ErrCounterCorrupt means the durable counter file exists but cannot be
// decoded.
var ErrCounterCorrupt = errors.New("netsec: durable counter file is corrupt")

// FileCounter persists the counter in a single file on local storage. Writes
// go through a temp file and an atomic rename, so a power loss leaves either
// the previous complete value or the new complete value, never a torn value.
type FileCounter struct {
	mu   sync.Mutex
	path string
}

// NewFileCounter loads (or lazily creates) the durable counter at path. A
// missing file is treated as counter zero; a present but undecodable file
// returns ErrCounterCorrupt rather than silently resetting.
func NewFileCounter(path string) (*FileCounter, error) {
	store := &FileCounter{path: path}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return store, nil
		}
		return nil, fmt.Errorf("netsec: stat counter file: %w", err)
	}
	if _, err := store.readLocked(); err != nil {
		return nil, err
	}
	return store, nil
}

// Current returns the durable counter value.
func (f *FileCounter) Current(context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readLocked()
}

// Commit atomically advances the durable counter when next is greater than the
// stored value.
func (f *FileCounter) Commit(ctx context.Context, next uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	current, err := f.readLocked()
	if err != nil {
		return err
	}
	if next <= current {
		return ErrCounterReplay
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	dir := filepath.Dir(f.path)
	tmp, err := os.CreateTemp(dir, ".counter-*")
	if err != nil {
		return fmt.Errorf("netsec: create temp counter: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], next)
	if _, err := tmp.Write(buf[:]); err != nil {
		tmp.Close()
		return fmt.Errorf("netsec: write counter: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("netsec: sync counter: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("netsec: close counter: %w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		return fmt.Errorf("netsec: rename counter: %w", err)
	}
	syncDirectory(dir)
	return nil
}

// readLocked decodes the counter file; the caller must hold f.mu.
func (f *FileCounter) readLocked() (uint64, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("netsec: read counter: %w", err)
	}
	if len(data) != 8 {
		return 0, ErrCounterCorrupt
	}
	return binary.BigEndian.Uint64(data), nil
}

// syncDirectory best-effort flushes the directory entry after rename. Errors
// are ignored because some filesystems do not support fsync on directories.
func syncDirectory(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

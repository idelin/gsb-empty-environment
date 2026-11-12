package policyauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// CounterStore is the monotonic authorization-generation counter, backed by
// TPM NV in production. Implementations must be safe for concurrent use.
type CounterStore interface {
	// Current returns the lowest acceptable token generation.
	Current(ctx context.Context) (uint64, error)
	// Advance records that generation was consumed; the stored value
	// becomes max(current, generation)+1 and is returned.
	Advance(ctx context.Context, generation uint64) (uint64, error)
}

// MemoryCounter is an in-process CounterStore, intended for tests and for
// services that persist the counter value out of band.
type MemoryCounter struct {
	mu    sync.Mutex
	value uint64
}

// NewMemoryCounter creates a counter starting at initial.
func NewMemoryCounter(initial uint64) *MemoryCounter {
	return &MemoryCounter{value: initial}
}

// Current returns the stored generation floor.
func (c *MemoryCounter) Current(context.Context) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, nil
}

// Advance moves the counter to max(current, generation)+1.
func (c *MemoryCounter) Advance(_ context.Context, generation uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation < c.value {
		return c.value, fmt.Errorf("%w: token generation %d, NV counter %d", ErrStaleGeneration, generation, c.value)
	}
	c.value = generation + 1
	return c.value, nil
}

// FileCounter persists the monotonic counter in a JSON file, a stand-in for
// the TPM NV index. Writes are atomic via a same-directory temp file so a
// crash during Advance cannot roll the counter backwards.
type FileCounter struct {
	mu   sync.Mutex
	path string
}

type counterFile struct {
	Generation uint64 `json:"generation"`
}

// NewFileCounter opens (or creates) a file-backed counter at path. A missing
// file means generation 0, the initial TPM NV counter value.
func NewFileCounter(path string) (*FileCounter, error) {
	if path == "" {
		return nil, errors.New("policyauth: empty counter file path")
	}
	counter := &FileCounter{path: path}
	if _, err := counter.load(); err != nil {
		return nil, err
	}
	return counter, nil
}

// Current returns the persisted generation floor.
func (c *FileCounter) Current(context.Context) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load()
}

// Advance persists max(current, generation)+1 atomically.
func (c *FileCounter) Advance(_ context.Context, generation uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current, err := c.load()
	if err != nil {
		return 0, err
	}
	if generation < current {
		return current, fmt.Errorf("%w: token generation %d, NV counter %d", ErrStaleGeneration, generation, current)
	}
	next := generation + 1
	if err := c.store(counterFile{Generation: next}); err != nil {
		return 0, err
	}
	return next, nil
}

func (c *FileCounter) load() (uint64, error) {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("policyauth: read counter file: %w", err)
	}
	var record counterFile
	if err := json.Unmarshal(data, &record); err != nil {
		return 0, fmt.Errorf("policyauth: parse counter file: %w", err)
	}
	return record.Generation, nil
}

func (c *FileCounter) store(record counterFile) error {
	dir := filepath.Dir(c.path)
	tempFile, err := os.CreateTemp(dir, ".counter-*.tmp")
	if err != nil {
		return fmt.Errorf("policyauth: create temp counter file: %w", err)
	}
	tempName := tempFile.Name()
	defer os.Remove(tempName)

	encoder := json.NewEncoder(tempFile)
	if err := encoder.Encode(record); err != nil {
		tempFile.Close()
		return fmt.Errorf("policyauth: write counter file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return fmt.Errorf("policyauth: sync counter file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("policyauth: close counter file: %w", err)
	}
	if err := os.Rename(tempName, c.path); err != nil {
		return fmt.Errorf("policyauth: replace counter file: %w", err)
	}
	syncDir(dir)
	return nil
}

// syncDir best-effort flushes the directory entry for the rename. On systems
// where syncing a directory is unsupported the error is ignored.
func syncDir(dir string) {
	dirFile, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = dirFile.Sync()
	_ = dirFile.Close()
}

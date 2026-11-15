package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store persists the complete journal. Load returns an empty slice for a new
// store. Save must atomically and durably replace the previous contents before
// returning nil; if Save returns an error, the previously stored journal must
// remain intact. The Engine calls Load once and Save serially.
type Store interface {
	Load() ([]byte, error)
	Save([]byte) error
}

// MemoryStore is a concurrency-safe in-memory Store, primarily useful for
// tests and short-lived hosts that provide durability elsewhere.
type MemoryStore struct {
	mu   sync.Mutex
	data []byte
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Load returns a copy of the stored journal.
func (s *MemoryStore) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.data...), nil
}

// Save replaces the stored journal.
func (s *MemoryStore) Save(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append([]byte(nil), data...)
	return nil
}

// FileStore stores the journal in a single file using write-to-temp, fsync,
// and atomic rename. The parent directory is created if necessary.
type FileStore struct {
	mu   sync.Mutex
	path string
}

// NewFileStore returns a FileStore rooted at path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Load returns the file contents, or an empty journal if the file does not
// exist yet.
func (s *FileStore) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Save atomically replaces the journal file.
func (s *FileStore) Save(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("netsec: replace journal: %w", err)
	}

	// Best effort: make the rename durable on filesystems that support
	// directory fsync. The data file itself has already been fsynced.
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

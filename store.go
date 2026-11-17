package netsec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store persists allocator snapshots. Load returns nil for an empty store. Save
// must atomically replace the previous snapshot or return an error without
// making a partial snapshot visible.
//
// Transactions span an in-memory update and one Save call, serialized by the
// Allocator. Multiple processes sharing a Store therefore need a Store backed by
// a database with compare-and-swap/transaction semantics; FileStore is intended
// for one allocator process at a time.
type Store interface {
	Load(ctx context.Context) ([]byte, error)
	Save(ctx context.Context, snapshot []byte) error
}

// MemoryStore is a non-durable Store for tests and single-process use.
type MemoryStore struct {
	mu   sync.Mutex
	data []byte
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Load implements Store.
func (s *MemoryStore) Load(ctx context.Context) ([]byte, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.data...), nil
}

// Save implements Store.
func (s *MemoryStore) Save(ctx context.Context, snapshot []byte) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append(s.data[:0], snapshot...)
	return nil
}

// FileStore atomically persists a JSON snapshot to path.
//
// Save writes a mode-0600 temporary file in the same directory, fsyncs it,
// renames it over path, and attempts to fsync the directory. Readers therefore
// observe either the old complete snapshot or the new complete snapshot, never
// a mixture.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a file-backed store. The parent directory is created
// with mode 0700 on the first save if it does not exist.
func NewFileStore(path string) (*FileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: empty store path", ErrInvalidOptions)
	}
	return &FileStore{path: path}, nil
}

// Load implements Store.
func (s *FileStore) Load(ctx context.Context) ([]byte, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Save implements Store.
func (s *FileStore) Save(ctx context.Context, snapshot []byte) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
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
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(snapshot); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	syncDir(dir)
	return nil
}

func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	defer handle.Close()
	_ = handle.Sync()
}

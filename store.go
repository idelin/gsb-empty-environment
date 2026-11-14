package netsec

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ScanStore persists sanitized image bytes under an opaque job identifier.
// Implementations must treat the identifier as an opaque capability and must
// not interpret it as a filesystem path.
type ScanStore interface {
	Put(jobID string, data []byte) error
	Get(jobID string) ([]byte, error)
	Delete(jobID string) error
}

// ErrStoreNotFound is returned by a ScanStore when no image exists.
var ErrStoreNotFound = errors.New("scan image not found")

// MemoryStore is a concurrency-safe in-memory ScanStore. It is the default
// backend and is suitable when the scan service already has durable object
// storage elsewhere.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]byte)}
}

// Put stores a private copy of data.
func (s *MemoryStore) Put(jobID string, data []byte) error {
	if s == nil {
		return ErrStoreNotFound
	}
	copied := append([]byte(nil), data...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	s.data[jobID] = copied
	return nil
}

// Get returns a private copy of the stored bytes.
func (s *MemoryStore) Get(jobID string) ([]byte, error) {
	if s == nil {
		return nil, ErrStoreNotFound
	}
	s.mu.RLock()
	data, ok := s.data[jobID]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrStoreNotFound
	}
	return append([]byte(nil), data...), nil
}

// Delete removes an image. Deleting a missing image is not an error.
func (s *MemoryStore) Delete(jobID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	delete(s.data, jobID)
	s.mu.Unlock()
	return nil
}

// DirStore persists scans as owner-only files inside a private directory.
// Job identifiers are validated again here so a corrupted registry can never
// turn into path traversal.
type DirStore struct {
	root string
}

// NewDirStore creates or opens a private scan directory.
func NewDirStore(root string) (*DirStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, ErrInternal
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, ErrInternal
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, ErrInternal
	}
	return &DirStore{root: root}, nil
}

func (s *DirStore) path(jobID string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(jobID)
	if err != nil || len(decoded) < 16 || base64.RawURLEncoding.EncodeToString(decoded) != jobID {
		return "", ErrStoreNotFound
	}
	return filepath.Join(s.root, jobID+".scan"), nil
}

// Put writes data atomically with owner-only permissions.
func (s *DirStore) Put(jobID string, data []byte) error {
	finalPath, err := s.path(jobID)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".scan-*")
	if err != nil {
		return ErrInternal
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return ErrInternal
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return ErrInternal
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return ErrInternal
	}
	if err := tmp.Close(); err != nil {
		return ErrInternal
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		return ErrInternal
	}
	return nil
}

// Get reads one scan while refusing symlinks and non-regular files.
func (s *DirStore) Get(jobID string) ([]byte, error) {
	path, err := s.path(jobID)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrStoreNotFound
		}
		return nil, ErrInternal
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStoreNotFound
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrInternal
	}
	return data, nil
}

// Delete removes one scan.
func (s *DirStore) Delete(jobID string) error {
	path, err := s.path(jobID)
	if err != nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return ErrInternal
	}
	return nil
}

// RandomJobID returns a new 256-bit URL-safe random identifier.
func RandomJobID() (string, error) {
	return randomJobID(rand.Reader, 32)
}

func randomJobID(reader io.Reader, size int) (string, error) {
	buf := make([]byte, size)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", ErrInternal
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

package netsec

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileStoreVersion = 1

// TrustedState is the complete durable state committed after a manifest is
// accepted. Manifest is the canonical manifest body covered by Digest.
// Generation is stored in the same atomic record as Manifest so recovery can
// never observe a new generation paired with old content.
type TrustedState struct {
	Generation uint64
	Digest     [sha256.Size]byte
	Manifest   []byte
}

// TrustedStore persists accepted manifest state. Implementations must make a
// single Commit call atomic: after a crash, Load must return either the old
// complete state or the new complete state, never a mixture.
type TrustedStore interface {
	Load() (TrustedState, error)
	Commit(next TrustedState) error
}

func validateTrustedState(state TrustedState) (Manifest, bool, error) {
	if state.Generation == 0 && len(state.Manifest) == 0 && state.Digest == ([sha256.Size]byte{}) {
		return Manifest{}, false, nil
	}
	if state.Generation == 0 {
		return Manifest{}, false, fmt.Errorf("%w: stored generation is zero", ErrStore)
	}
	if len(state.Manifest) == 0 {
		return Manifest{}, false, fmt.Errorf("%w: stored manifest is empty", ErrStore)
	}
	sum := sha256.Sum256(state.Manifest)
	if sum != state.Digest {
		return Manifest{}, false, fmt.Errorf("%w: stored manifest digest mismatch", ErrStore)
	}
	manifest, err := decodeManifestBody(state.Manifest)
	if err != nil {
		return Manifest{}, false, fmt.Errorf("%w: stored manifest is invalid: %v", ErrStore, err)
	}
	if manifest.Generation != state.Generation {
		return Manifest{}, false, fmt.Errorf("%w: stored generation %d does not match manifest generation %d", ErrStore, state.Generation, manifest.Generation)
	}
	return manifest, true, nil
}

func cloneTrustedState(state TrustedState) TrustedState {
	state.Manifest = append([]byte(nil), state.Manifest...)
	return state
}

// MemoryStore is an in-process TrustedStore. Commits replace one complete
// state while holding a mutex.
type MemoryStore struct {
	mu    sync.Mutex
	state TrustedState
}

// NewMemoryStore returns an empty in-memory trusted store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Load returns a copy of the committed state.
func (s *MemoryStore) Load() (TrustedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTrustedState(s.state), nil
}

// Commit atomically replaces the complete state.
func (s *MemoryStore) Commit(next TrustedState) error {
	if _, accepted, err := validateTrustedState(next); err != nil {
		return err
	} else if !accepted {
		return fmt.Errorf("%w: refusing to commit empty state", ErrStore)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = cloneTrustedState(next)
	return nil
}

// FileStore persists TrustedState in one atomically renamed file. A missing
// file represents an empty store.
type FileStore struct {
	Path string
}

// NewFileStore returns a file-backed trusted store. The parent directory must
// already exist.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty file path", ErrStore)
	}
	return &FileStore{Path: filepath.Clean(path)}, nil
}

type fileStoreRecord struct {
	Version    int      `json:"version"`
	Generation uint64   `json:"generation"`
	Digest     [32]byte `json:"digest"`
	Manifest   []byte   `json:"manifest"`
}

// Load reads and validates the committed record. Files left behind by a crash
// before rename have temporary names and are intentionally ignored.
func (s *FileStore) Load() (TrustedState, error) {
	if s == nil || s.Path == "" {
		return TrustedState{}, fmt.Errorf("%w: empty file path", ErrStore)
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return TrustedState{}, nil
	}
	if err != nil {
		return TrustedState{}, fmt.Errorf("%w: read state: %v", ErrStore, err)
	}

	var record fileStoreRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return TrustedState{}, fmt.Errorf("%w: decode state: %v", ErrStore, err)
	}
	if record.Version != fileStoreVersion {
		return TrustedState{}, fmt.Errorf("%w: unsupported state version %d", ErrStore, record.Version)
	}
	state := TrustedState{
		Generation: record.Generation,
		Digest:     record.Digest,
		Manifest:   record.Manifest,
	}
	if _, accepted, err := validateTrustedState(state); err != nil {
		return TrustedState{}, err
	} else if !accepted {
		return TrustedState{}, fmt.Errorf("%w: stored file has no accepted state", ErrStore)
	}
	return cloneTrustedState(state), nil
}

// Commit writes a complete new record to a temporary file, fsyncs it, renames
// it over the old record, and fsyncs the directory. The generation, digest,
// and canonical manifest content therefore become visible together.
func (s *FileStore) Commit(next TrustedState) error {
	if s == nil || s.Path == "" {
		return fmt.Errorf("%w: empty file path", ErrStore)
	}
	if _, accepted, err := validateTrustedState(next); err != nil {
		return err
	} else if !accepted {
		return fmt.Errorf("%w: refusing to commit empty state", ErrStore)
	}

	record := fileStoreRecord{
		Version:    fileStoreVersion,
		Generation: next.Generation,
		Digest:     next.Digest,
		Manifest:   append([]byte(nil), next.Manifest...),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("%w: encode state: %v", ErrStore, err)
	}

	directory := filepath.Dir(s.Path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(s.Path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("%w: create temporary state: %v", ErrStore, err)
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(temporaryName)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("%w: chmod temporary state: %v", ErrStore, err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("%w: write temporary state: %v", ErrStore, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("%w: sync temporary state: %v", ErrStore, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close temporary state: %v", ErrStore, err)
	}
	if err := os.Rename(temporaryName, s.Path); err != nil {
		return fmt.Errorf("%w: replace state: %v", ErrStore, err)
	}
	committed = true

	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("%w: open state directory: %v", ErrStore, err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("%w: sync state directory: %v", ErrStore, err)
	}
	return nil
}

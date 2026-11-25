package manifest

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Store is the durable trusted-storage abstraction. A commit is atomic: after
// Commit returns successfully, Load observes exactly the committed state; if
// Commit fails, the previously committed state remains observable. Under no
// circumstance may a new generation become visible with old manifest content.
type Store interface {
	// Load returns the current trusted state. It returns ErrNoState when no
	// manifest has ever been accepted.
	Load() (*State, error)
	// Commit atomically replaces the stored state.
	Commit(state *State) error
}

// FileStore is a file-backed Store. State is serialized to a temporary file in
// the same directory, fsynced, and atomically renamed over the state file, so
// a crash at any point leaves either the previous complete state or the new
// complete state on disk — never a mixture.
type FileStore struct {
	dir      string
	filePath string
	tmpPath  string
}

// NewFileStore creates (or opens) a FileStore rooted at dir. The directory is
// created if missing; a stale temporary file from an interrupted commit is
// removed during initialization and after every load.
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty store directory", ErrInvalidConfig)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("manifest: create store dir: %w", err)
	}
	store := &FileStore{
		dir:      dir,
		filePath: filepath.Join(dir, "manifest-state.gob"),
		tmpPath:  filepath.Join(dir, "manifest-state.gob.tmp"),
	}
	if err := store.removeStaleTemp(); err != nil {
		return nil, err
	}
	return store, nil
}

// Load reads and deserializes the committed state.
func (s *FileStore) Load() (*State, error) {
	if err := s.removeStaleTemp(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.filePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoState
	}
	if err != nil {
		return nil, fmt.Errorf("manifest: read state: %w", err)
	}

	var state State
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&state); err != nil {
		return nil, fmt.Errorf("manifest: decode state: %w", err)
	}
	if state.SignedManifest == nil {
		return nil, fmt.Errorf("%w: stored manifest is nil", ErrDigestMismatch)
	}
	return &state, nil
}

// Commit serializes state to a temporary file, fsyncs it, and renames it over
// the state file. The directory is fsynced afterwards so the rename itself is
// durable. Any failure leaves the previous state file intact.
func (s *FileStore) Commit(state *State) error {
	if state == nil || state.SignedManifest == nil || state.SignedManifest.Manifest == nil {
		return fmt.Errorf("%w: refusing to persist incomplete state", ErrInvalidConfig)
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		return fmt.Errorf("manifest: encode state: %w", err)
	}

	// Remove any leftover temporary file, then create a fresh one with
	// restrictive permissions in the same directory (same filesystem makes
	// rename atomic).
	if err := os.Remove(s.tmpPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("manifest: clean temp state: %w", err)
	}

	f, err := os.OpenFile(s.tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("manifest: create temp state: %w", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		os.Remove(s.tmpPath)
		return fmt.Errorf("manifest: write temp state: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(s.tmpPath)
		return fmt.Errorf("manifest: fsync temp state: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(s.tmpPath)
		return fmt.Errorf("manifest: close temp state: %w", err)
	}

	if err := os.Rename(s.tmpPath, s.filePath); err != nil {
		os.Remove(s.tmpPath)
		return fmt.Errorf("manifest: atomic rename state: %w", err)
	}

	// Fsync the directory so the rename entry is durable across power loss.
	dir, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("manifest: open store dir for fsync: %w", err)
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return fmt.Errorf("manifest: fsync store dir: %w", err)
	}
	return nil
}

func (s *FileStore) removeStaleTemp() error {
	err := os.Remove(s.tmpPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("manifest: remove stale temp state: %w", err)
	}
	return nil
}

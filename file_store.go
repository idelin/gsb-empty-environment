package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileStore persists each account as a JSON file and replaces files with an
// atomic rename after fsyncing their contents.
type FileStore struct {
	dir string
}

// NewFileStore creates dir if necessary and returns a durable store rooted
// there.
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty store directory", ErrStorage)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: create store directory: %v", ErrStorage, err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path(accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

// Load implements Store.
func (s *FileStore) Load(accountID string) (*AccountState, error) {
	data, err := os.ReadFile(s.path(accountID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read account: %v", ErrStorage, err)
	}
	var state AccountState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("%w: decode account: %v", ErrStorage, err)
	}
	if state.Pending == nil {
		state.Pending = make(map[string]PendingChange)
	}
	if state.Sessions == nil {
		state.Sessions = make(map[string]Session)
	}
	return &state, nil
}

// Save implements Store.
func (s *FileStore) Save(state *AccountState) error {
	if state == nil {
		return fmt.Errorf("%w: nil account state", ErrStorage)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("%w: encode account: %v", ErrStorage, err)
	}

	tmp, err := os.CreateTemp(s.dir, ".account-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: create temporary file: %v", ErrStorage, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: chmod temporary file: %v", ErrStorage, err)
	}
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: write account: %v", ErrStorage, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: sync account: %v", ErrStorage, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: close account: %v", ErrStorage, err)
	}
	if err := os.Rename(tmpName, s.path(state.ID)); err != nil {
		return fmt.Errorf("%w: commit account: %v", ErrStorage, err)
	}
	committed = true

	if dir, err := os.Open(s.dir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

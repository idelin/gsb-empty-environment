package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// loadState reads the persisted state file. A missing file yields
// (nil, nil) so that Open can bootstrap a fresh state.
func loadState(path string) (*state, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("netsec: read state file: %w", err)
	}

	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("netsec: decode state file: %w", err)
	}
	if st.Version != currentVersion {
		return nil, fmt.Errorf("netsec: unsupported state version %d", st.Version)
	}
	if st.Trusted == nil {
		st.Trusted = map[KeyID][]byte{}
	}
	if st.Candidates == nil {
		st.Candidates = map[KeyID]*anchor{}
	}
	if st.Revoked == nil {
		st.Revoked = map[KeyID]bool{}
	}
	return &st, nil
}

// saveState persists the state atomically: the JSON document is written
// to a temporary file in the same directory, fsynced and then renamed
// over the state file, so a crash never leaves a torn document.
func saveState(path string, st *state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("netsec: encode state: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".anchor-state-*.tmp")
	if err != nil {
		return fmt.Errorf("netsec: create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("netsec: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("netsec: sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("netsec: close state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("netsec: rename state: %w", err)
	}
	return nil
}

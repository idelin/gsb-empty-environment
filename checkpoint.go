package paradl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Checkpoint is the durable resume state. It is only valid for the entity
// version and size it was written against.
type Checkpoint struct {
	Version   string       `json:"version"`
	Size      int64        `json:"size"`
	ChunkSize int64        `json:"chunk_size"`
	Confirmed []RangeEntry `json:"confirmed"`
}

// SaveCheckpoint atomically persists ck to path: write to a temp file in the
// same directory, fsync, then rename over the destination. A crash at any
// point leaves either the old or the new checkpoint, never a torn one.
func SaveCheckpoint(path string, ck *Checkpoint) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ckpt-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp checkpoint: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename

	data, err := json.MarshalIndent(ck, "", "  ")
	if err != nil {
		tmp.Close()
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp checkpoint: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync temp checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp checkpoint: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename checkpoint: %w", err)
	}
	return nil
}

// ErrNoCheckpoint is returned when no checkpoint file exists.
var ErrNoCheckpoint = errors.New("no checkpoint file")

// LoadCheckpoint reads a checkpoint file. A corrupt file is reported as an
// error so the caller can discard it and restart the round from scratch.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCheckpoint
	}
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}
	var ck Checkpoint
	if err := json.Unmarshal(data, &ck); err != nil {
		return nil, fmt.Errorf("corrupt checkpoint %s: %w", path, err)
	}
	return &ck, nil
}

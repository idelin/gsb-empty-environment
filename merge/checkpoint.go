package merge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Checkpoint captures confirmed, durable progress. Every output byte up to
// OutputOffset is fsynced, and each input has been consumed up to its
// offset. Anything beyond these offsets may be redone after a crash.
type Checkpoint struct {
	OutputOffset  int64    `json:"output_offset"`
	OutputRecords uint64   `json:"output_records"`
	InputOffsets  []int64  `json:"input_offsets"`
	InputRecords  []uint64 `json:"input_records"`
	Truncated     []bool   `json:"truncated,omitempty"`
	Done          bool     `json:"done"`
}

// SaveCheckpoint writes ck atomically: tmp file, fsync, rename, dir fsync.
func SaveCheckpoint(path string, ck *Checkpoint) error {
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(ck, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}

// LoadCheckpoint reads the checkpoint at path, returning (nil, nil) if none
// exists.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ck Checkpoint
	if err := json.Unmarshal(data, &ck); err != nil {
		return nil, fmt.Errorf("bad checkpoint %s: %w", path, err)
	}
	return &ck, nil
}

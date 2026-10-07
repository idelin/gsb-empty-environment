package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Checkpoint records the importer's durable progress. All three fields are
// required: an offset alone is not a valid recovery point, because it cannot
// prove that the source prefix is unchanged or that the target received the
// corresponding batches.
type Checkpoint struct {
	// Offset is the byte offset in the source file up to which records have
	// been committed. It always sits on a record boundary.
	Offset int64 `json:"offset"`
	// PrefixDigest is the hex-encoded SHA-256 of source[:Offset].
	PrefixDigest string `json:"prefix_digest"`
	// Batch is the last batch number committed to the target.
	Batch uint64 `json:"batch"`
}

// ErrInvalidCheckpoint is returned when a checkpoint file exists but does not
// contain a complete recovery point (offset, prefix digest and batch number).
var ErrInvalidCheckpoint = errors.New("importer: invalid checkpoint")

func (c Checkpoint) valid() error {
	if c.Offset < 0 {
		return fmt.Errorf("%w: negative offset %d", ErrInvalidCheckpoint, c.Offset)
	}
	if c.PrefixDigest == "" {
		return fmt.Errorf("%w: missing prefix digest (offset-only checkpoints are not recoverable)", ErrInvalidCheckpoint)
	}
	return nil
}

// loadCheckpoint reads the checkpoint file. The second return value reports
// whether a checkpoint file existed at all.
func loadCheckpoint(path string) (Checkpoint, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, true, fmt.Errorf("%w: %v", ErrInvalidCheckpoint, err)
	}
	if err := cp.valid(); err != nil {
		return Checkpoint{}, true, err
	}
	return cp, true, nil
}

// saveCheckpoint writes the checkpoint atomically (temp file + rename) so a
// crash never leaves a half-written recovery point.
func saveCheckpoint(path string, cp Checkpoint) error {
	if err := cp.valid(); err != nil {
		return err
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

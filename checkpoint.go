package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	phaseRuns  = "runs"
	phaseMerge = "merge"
	phaseDone  = "done"
)

// Checkpoint is the durable state of a merge job. It is written atomically
// (tmp file + fsync + rename) and only after the data it references (run files
// or output bytes) has been fsynced.
type Checkpoint struct {
	Phase   string   `json:"phase"`
	Sources []string `json:"sources"`
	Runs    []string `json:"runs,omitempty"`

	// runs phase: position of the next input record to read.
	SrcIdx    int   `json:"srcIdx"`
	SrcOffset int64 `json:"srcOffset"`
	Seq       int   `json:"seq"`

	// merge phase: consumed byte offset per run file, and the acknowledged
	// output length. Output bytes beyond OutOffset are unacknowledged and are
	// truncated on resume.
	RunOffsets []int64 `json:"runOffsets,omitempty"`
	OutOffset  int64   `json:"outOffset"`
	OutRecords int64   `json:"outRecords"`
}

func checkpointPath(dir string) string { return filepath.Join(dir, "checkpoint.json") }

func loadCheckpoint(dir string) (*Checkpoint, error) {
	data, err := os.ReadFile(checkpointPath(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ck Checkpoint
	if err := json.Unmarshal(data, &ck); err != nil {
		return nil, fmt.Errorf("corrupt checkpoint: %w", err)
	}
	return &ck, nil
}

func saveCheckpoint(dir string, ck *Checkpoint) error {
	data, err := json.MarshalIndent(ck, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "checkpoint.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
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
	if err := os.Rename(tmp, checkpointPath(dir)); err != nil {
		return err
	}
	// Best-effort directory fsync so the rename itself is durable.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

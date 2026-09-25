package kvimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// checkpoint is the durable recovery record. It is written atomically
// (tmp file + fsync + rename + directory fsync) once per confirmed batch.
//
// The checkpoint pins the identity of the input (path, size, mtime, head
// digest) plus a running digest of every consumed byte up to Offset, so
// recovery can prove the input has not changed before resuming.
type checkpoint struct {
	InputPath    string `json:"input_path"`
	Size         int64  `json:"size"`
	MtimeNano    int64  `json:"mtime_nano"`
	HeadSHA256   string `json:"head_sha256"`   // sha256 of the first headHashBytes of input
	Offset       int64  `json:"offset"`        // last confirmed input offset (bytes consumed)
	PrefixSHA256 string `json:"prefix_sha256"` // sha256 of input[0:Offset]
	WalLen       int64  `json:"wal_len"`       // confirmed length of data.wal
	RejectsLen   int64  `json:"rejects_len"`   // confirmed length of rejects.log
	Done         bool   `json:"done"`          // set only after the store has been published
}

// InputChangedError is returned when the input file no longer matches the
// identity recorded in the checkpoint. Recovery is refused.
type InputChangedError struct {
	Reason string
}

func (e *InputChangedError) Error() string {
	return "kvimport: input changed since last checkpoint: " + e.Reason
}

// IsInputChanged reports whether err refuses recovery because the input
// file changed.
func IsInputChanged(err error) bool {
	var ice *InputChangedError
	return errors.As(err, &ice)
}

func loadCheckpoint(dir string) (*checkpoint, error) {
	data, err := os.ReadFile(filepath.Join(dir, checkpointName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}
	var ck checkpoint
	if err := json.Unmarshal(data, &ck); err != nil {
		// A torn or corrupt checkpoint.json should be impossible because
		// writes are atomic; refuse to guess.
		return nil, fmt.Errorf("corrupt checkpoint, refusing to recover: %w", err)
	}
	return &ck, nil
}

func saveCheckpoint(dir string, ck *checkpoint) error {
	data, err := json.MarshalIndent(ck, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(dir, checkpointName, data)
}

// writeFileAtomic writes data to dir/name durably and atomically:
// write to a temp file in the same directory, fsync it, rename over the
// target, then fsync the directory so the rename itself is durable.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp := filepath.Join(dir, name+".tmp")
	final := filepath.Join(dir, name)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

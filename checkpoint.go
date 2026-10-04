package extsort

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// InputBinding pins one merge input segment to its content digest, so a
// checkpoint is only ever resumed against the exact bytes it was taken
// with. Unsynced or replaced segments are detected, never trusted.
type InputBinding struct {
	File    string `json:"file"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Records int64  `json:"records"`
}

func (b InputBinding) equal(o InputBinding) bool {
	return b.File == o.File && b.Size == o.Size && b.SHA256 == o.SHA256 && b.Records == o.Records
}

// Cursor is a resumable read position inside one input segment.
type Cursor struct {
	Offset  int64 `json:"offset"`
	Records int64 `json:"records"`
}

// OutputBinding pins the published output prefix: only the first Length
// bytes (whose digest is SHA256) were synced and may be trusted; anything
// past Length is unsynced buffer residue and is truncated on resume.
type OutputBinding struct {
	File    string `json:"file"`
	Length  int64  `json:"length"`
	Records int64  `json:"records"`
	SHA256  string `json:"sha256"`
}

// Checkpoint binds input digests, per-input cursors and the published
// output prefix of one merge task.
type Checkpoint struct {
	Version int            `json:"version"`
	Inputs  []InputBinding `json:"inputs"`
	Cursors []Cursor       `json:"cursors"`
	Output  OutputBinding  `json:"output"`
}

const checkpointVersion = 1

// writeFileAtomic writes data to path via tmp+fsync+rename+dir-fsync so a
// crash never leaves a half-written checkpoint or manifest behind.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := countedOpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := countedOpen(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func saveJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// digestFile computes the SHA-256 of a file opened through the counted
// helpers (included in peak-open-file accounting).
func digestFile(path string) (string, int64, error) {
	f, err := countedOpen(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// verifyBinding checks that the file at dir/binding.File still has exactly
// the bound size and digest.
func verifyBinding(dir string, b InputBinding) error {
	sum, size, err := digestFile(filepath.Join(dir, b.File))
	if err != nil {
		return fmt.Errorf("verify %s: %w", b.File, err)
	}
	if size != b.Size {
		return fmt.Errorf("verify %s: size changed: bound %d, found %d", b.File, b.Size, size)
	}
	if sum != b.SHA256 {
		return fmt.Errorf("verify %s: digest mismatch: bound %s, found %s", b.File, b.SHA256, sum)
	}
	return nil
}

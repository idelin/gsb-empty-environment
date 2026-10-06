package dl

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ErrCrash is returned when a fault-injected crash fires inside checkpoint
// saving. The process is assumed dead; callers restart and resume.
var ErrCrash = errors.New("dl: simulated crash")

// checkpoint is the on-disk resume state. Every confirmed chunk belongs to
// one entity version; a version change invalidates the whole file.
type checkpoint struct {
	ETag      string `json:"etag"`
	Size      int64  `json:"size"`
	ChunkSize int64  `json:"chunk_size"`
	Confirmed []int  `json:"confirmed"`
}

// saveCheckpointLocked atomically persists the confirmed set:
// write temp file, fsync, rename, fsync dir. Crash hooks sit at the three
// cut points. Caller must hold d.mu.
func (d *Downloader) saveCheckpointLocked() error {
	d.ckptSaveCount++
	saveNo := d.ckptSaveCount

	cp := checkpoint{
		ETag:      d.manifest.ETag,
		Size:      d.manifest.Size,
		ChunkSize: d.manifest.ChunkSize,
		Confirmed: make([]int, 0, d.confirmedCnt),
	}
	for i, ok := range d.confirmed {
		if ok {
			cp.Confirmed = append(cp.Confirmed, i)
		}
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}

	tmp := d.ckptPath + ".tmp"
	if d.faults.shouldCrash("before_write", saveNo) {
		return ErrCrash
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

	if d.faults.shouldCrash("before_rename", saveNo) {
		return ErrCrash
	}
	if err := os.Rename(tmp, d.ckptPath); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(d.ckptPath)); err == nil {
		dir.Sync()
		dir.Close()
	}

	if d.faults.shouldCrash("after_rename", saveNo) {
		return ErrCrash
	}
	return nil
}

// loadCheckpoint reads and validates the checkpoint against the current
// manifest, then re-verifies every confirmed chunk by hashing the bytes on
// disk. Chunks that fail verification are dropped and will be refetched.
// A checkpoint from another entity version is discarded entirely.
func (d *Downloader) loadCheckpoint() {
	data, err := os.ReadFile(d.ckptPath)
	if err != nil {
		return
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		os.Remove(d.ckptPath)
		return
	}
	if cp.ETag != d.manifest.ETag || cp.Size != d.manifest.Size || cp.ChunkSize != d.manifest.ChunkSize {
		os.Remove(d.ckptPath)
		return
	}
	for _, idx := range cp.Confirmed {
		if idx < 0 || idx >= len(d.confirmed) || d.confirmed[idx] {
			continue
		}
		if d.verifyChunk(idx) {
			d.confirmed[idx] = true
			d.confirmedCnt++
		}
	}
}

package tailfollow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// FileID identifies a file by device number and inode, so it stays
// valid across renames and changes when a path is replaced.
type FileID struct {
	Dev uint64 `json:"dev"`
	Ino uint64 `json:"ino"`
}

// Checkpoint records how far a file has been delivered.
type Checkpoint struct {
	ID     FileID `json:"id"`
	Offset int64  `json:"offset"`
}

func fileID(info os.FileInfo) FileID {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return FileID{Dev: uint64(st.Dev), Ino: st.Ino}
	}
	return FileID{}
}

// SaveCheckpoint atomically persists ck by writing a temp file and
// renaming it over the destination.
func SaveCheckpoint(path string, ck Checkpoint) error {
	data, err := json.Marshal(ck)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
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
	return os.Rename(tmp, path)
}

// LoadCheckpoint reads a checkpoint; a missing file yields a zero one.
func LoadCheckpoint(path string) (Checkpoint, error) {
	var ck Checkpoint
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ck, nil
	}
	if err != nil {
		return ck, err
	}
	if err := json.Unmarshal(data, &ck); err != nil {
		return Checkpoint{}, err
	}
	return ck, nil
}

// Config controls a Follower.
type Config struct {
	Path           string        // log file to follow
	CheckpointPath string        // where progress is persisted
	PollInterval   time.Duration // default 50ms
	LineTimeout    time.Duration // flush a partial line after this; default 1s
}

// Follower tails a file, surviving rename rotation and copytruncate.
type Follower struct {
	cfg Config
	out chan string

	f            *os.File
	id           FileID
	readPos      int64 // bytes consumed from f, including pending partial
	partial      []byte
	partialSince time.Time
	ckpt         Checkpoint
}

// New creates a Follower; delivered lines are read from Lines().
func New(cfg Config) *Follower {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 50 * time.Millisecond
	}
	if cfg.LineTimeout <= 0 {
		cfg.LineTimeout = time.Second
	}
	return &Follower{cfg: cfg, out: make(chan string, 1024)}
}

// Lines returns the channel of delivered lines, closed when Run exits.
func (fw *Follower) Lines() <-chan string { return fw.out }

// Run follows the file until ctx is cancelled, then saves a final
// checkpoint and closes Lines().
func (fw *Follower) Run(ctx context.Context) error {
	defer close(fw.out)

	ck, err := LoadCheckpoint(fw.cfg.CheckpointPath)
	if err != nil {
		return err
	}
	fw.ckpt = ck
	if info, statErr := os.Stat(fw.cfg.Path); statErr == nil {
		id := fileID(info)
		var offset int64
		if ck.ID == id {
			offset = ck.Offset
			if offset > info.Size() {
				offset = 0 // file shrank while we were away
			}
		}
		if err := fw.openAt(id, offset); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(fw.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if err := fw.poll(ctx); err != nil {
			fw.save()
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			fw.save()
			return nil
		case <-ticker.C:
		}
	}
}

func (fw *Follower) openAt(id FileID, offset int64) error {
	f, err := os.Open(fw.cfg.Path)
	if err != nil {
		return err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	fw.f = f
	fw.id = id
	fw.readPos = offset
	return nil
}

func (fw *Follower) poll(ctx context.Context) error {
	pathInfo, pathErr := os.Stat(fw.cfg.Path)

	if fw.f == nil {
		if pathErr != nil {
			return nil // wait for the file to appear
		}
		if err := fw.openAt(fileID(pathInfo), 0); err != nil {
			return err
		}
	} else if pathErr == nil && fileID(pathInfo) != fw.id {
		// Renamed away and replaced: drain the old file completely
		// before switching to whatever now lives at the path. While
		// the path is missing we keep reading the old descriptor, so
		// writers still appending to the rotated file are not lost.
		if err := fw.readAvailable(ctx); err != nil {
			return err
		}
		fw.f.Close()
		fw.f = nil
		fw.id = FileID{}
		return nil
	} else if fi, err := fw.f.Stat(); err == nil && fi.Size() < fw.readPos {
		// copytruncate: same inode, contents replaced in place.
		// Detected by shrinkage; a rewrite to the exact same size is
		// inherently indistinguishable from no writes at all.
		if err := fw.flushPartial(ctx); err != nil {
			return err
		}
		if _, err := fw.f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		fw.readPos = 0
	}

	if err := fw.readAvailable(ctx); err != nil {
		return err
	}
	if len(fw.partial) > 0 && time.Since(fw.partialSince) >= fw.cfg.LineTimeout {
		if err := fw.flushPartial(ctx); err != nil {
			return err
		}
	}
	fw.save()
	return nil
}

func (fw *Follower) readAvailable(ctx context.Context) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := fw.f.Read(buf)
		if n > 0 {
			if perr := fw.process(ctx, buf[:n]); perr != nil {
				return perr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

// process consumes data, emitting complete lines. readPos only advances
// past bytes that are emitted or buffered, so a cancelled emit is
// redelivered after restart rather than lost.
func (fw *Follower) process(ctx context.Context, data []byte) error {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			fw.partial = append(fw.partial, data...)
			fw.partialSince = time.Now()
			fw.readPos += int64(len(data))
			return nil
		}
		var line string
		if len(fw.partial) > 0 {
			line = string(fw.partial) + string(data[:i])
		} else {
			line = string(data[:i])
		}
		if err := fw.emit(ctx, line); err != nil {
			return err
		}
		fw.partial = fw.partial[:0]
		fw.readPos += int64(i + 1)
		data = data[i+1:]
	}
	return nil
}

func (fw *Follower) flushPartial(ctx context.Context) error {
	if len(fw.partial) == 0 {
		return nil
	}
	line := string(fw.partial)
	if err := fw.emit(ctx, line); err != nil {
		return err
	}
	fw.partial = fw.partial[:0]
	return nil
}

func (fw *Follower) emit(ctx context.Context, line string) error {
	select {
	case fw.out <- line:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// save persists the checkpoint if progress moved. The stored offset
// excludes the pending partial line so it is re-read after a restart.
func (fw *Follower) save() {
	if fw.f == nil {
		return
	}
	ck := Checkpoint{ID: fw.id, Offset: fw.readPos - int64(len(fw.partial))}
	if ck == fw.ckpt {
		return
	}
	if err := SaveCheckpoint(fw.cfg.CheckpointPath, ck); err == nil {
		fw.ckpt = ck
	}
}

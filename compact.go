package batchlease

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Compact rewrites the log as a single snapshot frame containing only the
// live state. The replacement is atomic: a new file is fully written and
// fsynced under a temporary name, then renamed over the old log and the
// directory entry is fsynced. A crash at any point is recoverable:
//
//   - before the rename: the old log is untouched and the temp file is
//     discarded on the next Open;
//   - after the rename: the new snapshot log is the durable state.
//
// Faults before the rename leave the Store fully usable. A fault at or after
// the replacement poisons the Store, because memory and disk may disagree;
// reopening recovers the committed state.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.readyLocked(); err != nil {
		return err
	}

	ids := make([]string, 0, len(s.leases))
	for id := range s.leases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	entries := make([]leaseEntry, 0, len(ids))
	for _, id := range ids {
		le := s.leases[id]
		entries = append(entries, *le)
	}
	payload := encodeSnapshot(s.epoch, s.firstSeq, s.confirmed, entries)
	frame := buildFrame(recSnapshot, payload)

	tmpPath := filepath.Join(s.dir, tmpName)
	// A stale temp file should already have been removed at Open, but
	// remove it again so a repeated Compact starts clean.
	if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("batchlease: remove compaction temp: %w", err)
	}

	if err := s.before(OpCreate); err != nil {
		return err
	}
	tf, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("batchlease: create compaction temp: %w", err)
	}

	// Writes to the temp file never poison the Store: until the rename the
	// committed log is the source of truth.
	if err := s.before(OpWrite); err != nil {
		_ = tf.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	n, err := tf.Write(frame)
	if err != nil {
		_ = tf.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("batchlease: write compaction temp: %w", err)
	}
	if n < len(frame) {
		_ = tf.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("batchlease: short compaction write: %w", ErrShortWrite)
	}
	if err := s.before(OpSync); err != nil {
		_ = tf.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tf.Sync(); err != nil {
		_ = tf.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("batchlease: sync compaction temp: %w", err)
	}
	if err := tf.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("batchlease: close compaction temp: %w", err)
	}

	// From the rename onward a failure means the on-disk layout changed
	// underneath the open file handle, so poison and require a reopen.
	if err := s.before(OpRename); err != nil {
		_ = os.Remove(tmpPath)
		return s.poison(OpRename, err)
	}
	if err := os.Rename(tmpPath, filepath.Join(s.dir, logName)); err != nil {
		_ = os.Remove(tmpPath)
		return s.poison(OpRename, err)
	}
	if err := syncDir(s.dir, s.hook); err != nil {
		return s.poison(OpDirSync, err)
	}

	if err := s.f.Close(); err != nil {
		return s.poison(OpRename, err)
	}
	f, err := os.OpenFile(filepath.Join(s.dir, logName), os.O_RDWR, 0o600)
	if err != nil {
		return s.poison(OpCreate, err)
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return s.poison(OpCreate, err)
	}
	s.f = f
	s.logSize = int64(len(frame))
	return nil
}

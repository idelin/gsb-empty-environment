package roaring

import (
	"errors"
	"fmt"
	"sync"
)

var errStoreClosed = errors.New("roaring: store is closed")

const (
	snapshotFile = "bitmap.snap"
	tmpFile      = "bitmap.snap.tmp"
	walFile      = "bitmap.wal"
)

// Store is a durable Bitmap. Every mutation is appended to a write-ahead log
// and fsynced before it is applied in memory, so a committed operation
// survives a crash. Checkpoint() compacts the WAL into a CRC-protected
// snapshot written atomically via tmp-file + rename.
//
// Store is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	fs     VFS
	bm     *Bitmap
	wal    VFile
	walOff int64
	closed bool
}

// OpenStore opens (or creates) a store on fs, recovering any committed state:
// it loads the snapshot if present, then replays the valid prefix of the
// WAL. A torn or corrupt WAL tail is truncated in place.
func OpenStore(fs VFS) (*Store, error) {
	s := &Store{fs: fs, bm: New()}
	data, err := fs.ReadFile(snapshotFile)
	switch {
	case err == nil:
		bm, err := unmarshalSnapshot(data)
		if err != nil {
			return nil, fmt.Errorf("roaring: snapshot: %w", err)
		}
		s.bm = bm
	case errors.Is(err, errNotExist):
	default:
		return nil, err
	}
	var walLen int64
	data, err = fs.ReadFile(walFile)
	switch {
	case err == nil:
		var applied int64
		for _, rec := range parseRecords(data) {
			if err := applyRecord(s.bm, rec.payload); err != nil {
				break
			}
			applied = rec.endOffset
		}
		if applied < int64(len(data)) {
			// Torn or corrupt tail: roll the WAL back to the last
			// valid record so future appends start from a clean offset.
			if err := fs.Truncate(walFile, applied); err != nil {
				return nil, err
			}
		}
		walLen = applied
	case errors.Is(err, errNotExist):
	default:
		return nil, err
	}
	f, err := fs.OpenFile(walFile, false)
	if err != nil {
		return nil, err
	}
	s.wal = f
	s.walOff = walLen
	return s, nil
}

// Add durably inserts the given values under the shared high-16-bit key.
func (s *Store) Add(key uint16, vals ...uint16) error {
	if len(vals) == 0 {
		return nil
	}
	return s.appendAndApply(encodeAddRecord(key, vals), func(bm *Bitmap) {
		for _, v := range vals {
			bm.Add(uint32(key)<<16 | uint32(v))
		}
	})
}

// AddRange durably inserts every value in [lo, hi] under key.
func (s *Store) AddRange(key uint16, lo, hi uint16) error {
	if lo > hi {
		return nil
	}
	return s.appendAndApply(encodeRangeRecord(key, lo, hi), func(bm *Bitmap) {
		bm.AddRange(key, lo, hi)
	})
}

// appendAndApply writes and fsyncs one WAL record, then applies it to the
// in-memory bitmap. If the write or sync fails (e.g. disk full), the WAL is
// truncated back and the in-memory state is left untouched: the operation is
// fully rolled back.
func (s *Store) appendAndApply(payload []byte, apply func(bm *Bitmap)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errStoreClosed
	}
	rec := frameRecord(payload)
	off := s.walOff
	if _, err := s.wal.Write(rec); err != nil {
		s.rollbackWALLocked(off)
		return fmt.Errorf("roaring: wal write failed, rolled back: %w", err)
	}
	if err := s.wal.Sync(); err != nil {
		s.rollbackWALLocked(off)
		return fmt.Errorf("roaring: wal sync failed, rolled back: %w", err)
	}
	s.walOff += int64(len(rec))
	apply(s.bm)
	return nil
}

func (s *Store) rollbackWALLocked(off int64) {
	_ = s.wal.Close()
	if err := s.fs.Truncate(walFile, off); err != nil {
		return
	}
	if f, err := s.fs.OpenFile(walFile, false); err == nil {
		s.wal = f
	}
}

// Checkpoint writes a compacted snapshot of the current state and resets the
// WAL. The snapshot is written to a temporary file, fsynced, and atomically
// renamed into place; a failure (e.g. disk full) leaves the previous
// snapshot and the WAL untouched.
func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errStoreClosed
	}
	data := marshalSnapshot(s.bm)
	f, err := s.fs.OpenFile(tmpFile, true)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = s.fs.Remove(tmpFile)
		return fmt.Errorf("roaring: snapshot write failed, old state preserved: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = s.fs.Remove(tmpFile)
		return fmt.Errorf("roaring: snapshot sync failed, old state preserved: %w", err)
	}
	_ = f.Close()
	if err := s.fs.Rename(tmpFile, snapshotFile); err != nil {
		return err
	}
	_ = s.wal.Close()
	if err := s.fs.Remove(walFile); err != nil {
		return err
	}
	f2, err := s.fs.OpenFile(walFile, false)
	if err != nil {
		return err
	}
	s.wal = f2
	s.walOff = 0
	return nil
}

// Bitmap returns a snapshot (deep copy) of the current in-memory state.
func (s *Store) Bitmap() *Bitmap {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bm.Clone()
}

// Close flushes and closes the WAL. The store must not be used afterwards.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.wal != nil {
		_ = s.wal.Sync()
		return s.wal.Close()
	}
	return nil
}

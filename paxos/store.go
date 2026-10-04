package paxos

import (
	"io"
	"os"
	"sync"
	"syscall"
)

// Store is an append-only, crash-safe persistent log. Every accepted entry
// is appended as a record; replaying collapses duplicates by keeping the
// highest-ballot record per slot (see LoadLog).
type Store struct {
	mu         sync.RWMutex
	f          *os.File
	path       string
	size       int64
	syncWrites bool
	closed     bool
}

// OpenStore opens (or creates) the log file at path. An existing file is
// validated and any torn/corrupt tail is truncated so the file always ends
// on a record boundary.
func OpenStore(path string, syncWrites bool) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Store{f: f, path: path, syncWrites: syncWrites}
	if err := s.init(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	st, err := s.f.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	if size > 0 && size < HeaderSize {
		// Unusable partial header: start over.
		if err := s.f.Truncate(0); err != nil {
			return err
		}
		size = 0
	}
	if size == 0 {
		if _, err := s.f.WriteAt(EncodeHeader(nil), 0); err != nil {
			return err
		}
		if s.syncWrites {
			if err := s.f.Sync(); err != nil {
				return err
			}
		}
		s.size = HeaderSize
		return nil
	}
	buf, unmap, err := mapFile(s.f, size)
	if err != nil {
		return err
	}
	defer unmap()
	if err := CheckHeader(buf); err != nil {
		return err
	}
	valid, scanErr := Scan(buf[HeaderSize:], nil)
	end := int64(HeaderSize + valid)
	if scanErr != nil || end < size {
		// Corrupt or torn tail: truncate to the last valid record.
		if err := s.f.Truncate(end); err != nil {
			return err
		}
	}
	s.size = end
	return nil
}

// Append durably appends one record.
func (s *Store) Append(e Entry) error {
	return s.AppendBatch([]Entry{e})
}

// AppendBatch appends several records with a single lock acquisition and at
// most one fsync.
func (s *Store) AppendBatch(es []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	var buf []byte
	for _, e := range es {
		buf = AppendRecord(buf, e)
	}
	if _, err := s.f.WriteAt(buf, s.size); err != nil {
		return err
	}
	s.size += int64(len(buf))
	if s.syncWrites {
		return s.f.Sync()
	}
	return nil
}

// Sync flushes buffered writes to disk.
func (s *Store) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	return s.f.Sync()
}

// Size returns the current valid file size in bytes.
func (s *Store) Size() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}

// View maps the file read-only and invokes fn for every valid record. The
// entries handed to fn alias the mapped pages directly (zero-copy) and must
// not be retained after View returns.
func (s *Store) View(fn func(off int64, e Entry) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return os.ErrClosed
	}
	buf, unmap, err := mapFile(s.f, s.size)
	if err != nil {
		return err
	}
	defer unmap()
	if err := CheckHeader(buf); err != nil {
		return err
	}
	_, err = Scan(buf[HeaderSize:s.size], fn)
	return err
}

// Load returns owned copies of all valid records in append order.
func (s *Store) Load() ([]Entry, error) {
	var out []Entry
	err := s.View(func(_ int64, e Entry) error {
		out = append(out, CloneEntry(e))
		return nil
	})
	return out, err
}

// LoadLog replays the file and collapses it into the live log view: for each
// slot, the record with the highest ballot wins.
func (s *Store) LoadLog() (map[uint64]Entry, error) {
	log := make(map[uint64]Entry)
	err := s.View(func(_ int64, e Entry) error {
		if cur, ok := log[e.Index]; !ok || entryBallotLess(cur, e) {
			log[e.Index] = CloneEntry(e)
		}
		return nil
	})
	return log, err
}

// Close releases the file. The store must not be used afterwards.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.f.Close()
}

// mapFile maps size bytes of f read-only. If mmap is unavailable it falls
// back to a plain read; either way the returned buffer is the sole copy and
// decoders alias it directly.
func mapFile(f *os.File, size int64) ([]byte, func(), error) {
	if size == 0 {
		return nil, func() {}, nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err == nil {
		return data, func() { syscall.Munmap(data) }, nil
	}
	if _, serr := f.Seek(0, io.SeekStart); serr != nil {
		return nil, func() {}, serr
	}
	buf := make([]byte, size)
	if _, rerr := io.ReadFull(f, buf); rerr != nil {
		return nil, func() {}, rerr
	}
	return buf, func() {}, nil
}

// Package store models a paged database file with a volatile buffer
// pool. Pages live in the buffer pool and must be explicitly flushed
// to become durable; Crash discards the buffer pool, simulating a
// power failure. The write-ahead logging rule is enforced: a dirty
// page is never written to disk before the log records describing it
// are durable.
package store

import (
	"fmt"
	"sync"

	"aries/wal"
)

// PageSize is the fixed page size in bytes.
const PageSize = 4096

// Page is an in-memory page image plus the LSN of the most recent
// log record applied to it.
type Page struct {
	Data    [PageSize]byte
	PageLSN uint64
	dirty   bool
}

// Store is a buffer pool over a durable page file.
type Store struct {
	mu   sync.Mutex
	wal  *wal.WAL
	disk map[uint64]*Page
	buf  map[uint64]*Page
}

// New creates a Store whose durability is tied to the given WAL.
func New(w *wal.WAL) *Store {
	return &Store{
		wal:  w,
		disk: make(map[uint64]*Page),
		buf:  make(map[uint64]*Page),
	}
}

// Fetch returns the buffer-pool copy of a page, loading it from disk
// (or creating a zero page) if necessary.
func (s *Store) Fetch(pageID uint64) *Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pg, ok := s.buf[pageID]; ok {
		return pg
	}
	pg := &Page{}
	if d, ok := s.disk[pageID]; ok {
		*pg = *d
	}
	s.buf[pageID] = pg
	return pg
}

// Write applies data at off inside the buffered page and stamps it
// with lsn. The page becomes dirty.
func (s *Store) Write(pageID uint64, off int, data []byte, lsn uint64) error {
	if off < 0 || off+len(data) > PageSize {
		return fmt.Errorf("store: write of %d bytes at offset %d out of range", len(data), off)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pg := s.buf[pageID]
	if pg == nil {
		pg = &Page{}
		if d, ok := s.disk[pageID]; ok {
			*pg = *d
		}
		s.buf[pageID] = pg
	}
	copy(pg.Data[off:], data)
	pg.PageLSN = lsn
	pg.dirty = true
	return nil
}

// FlushPage makes a buffered page durable, flushing the WAL first if
// the page's LSN is not yet covered by the durable log (WAL rule).
func (s *Store) FlushPage(pageID uint64) error {
	s.mu.Lock()
	pg := s.buf[pageID]
	if pg == nil || !pg.dirty {
		s.mu.Unlock()
		return nil
	}
	need := pg.PageLSN
	s.mu.Unlock()

	if s.wal.FlushedLSN() <= need {
		if err := s.wal.Flush(); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	pg = s.buf[pageID]
	cp := &Page{}
	*cp = *pg
	s.disk[pageID] = cp
	pg.dirty = false
	return nil
}

// FlushAll flushes every dirty page in the buffer pool.
func (s *Store) FlushAll() error {
	s.mu.Lock()
	ids := make([]uint64, 0, len(s.buf))
	for id, pg := range s.buf {
		if pg.dirty {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.FlushPage(id); err != nil {
			return err
		}
	}
	return nil
}

// Crash simulates a power failure: all unflushed buffer-pool content
// is lost. The WAL is assumed durable up to its last Flush.
func (s *Store) Crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = make(map[uint64]*Page)
}

// DiskPage returns the durable image of a page (for verification).
func (s *Store) DiskPage(pageID uint64) (data []byte, pageLSN uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pg := s.disk[pageID]
	if pg == nil {
		return make([]byte, PageSize), 0
	}
	out := make([]byte, PageSize)
	copy(out, pg.Data[:])
	return out, pg.PageLSN
}

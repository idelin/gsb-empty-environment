package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrRevoked means that a snapshot has already released its page
	// references.
	ErrRevoked = errors.New("netsec: snapshot revoked")
	// ErrOutOfBounds means that an access falls outside the snapshot.
	ErrOutOfBounds = errors.New("netsec: access out of bounds")
)

// Snapshot is an authorized view of a set of physical pages. Forked snapshots
// share pages; the first write to a shared page creates a private copy.
type Snapshot struct {
	mu         sync.RWMutex
	pool       *Pool
	pageSize   int
	size       int64
	pages      []PageID
	authorized bool
	releaseErr error
	released   chan struct{}
}

// NewSnapshot copies data into pages and returns the initial authorized
// snapshot.
func (p *Pool) NewSnapshot(data []byte) (*Snapshot, error) {
	ids, err := p.newSnapshot(data)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		pool:       p,
		pageSize:   p.pageSize,
		size:       int64(len(data)),
		pages:      ids,
		authorized: true,
	}, nil
}

// Size returns the logical snapshot size in bytes.
func (s *Snapshot) Size() int64 {
	return s.size
}

// Len returns the logical snapshot size as an int.
func (s *Snapshot) Len() int {
	return int(s.size)
}

// PageSize returns the physical page size used by the snapshot.
func (s *Snapshot) PageSize() int {
	return s.pageSize
}

// Authorized reports whether the snapshot still owns its page references.
func (s *Snapshot) Authorized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.authorized
}

// Fork creates another authorized snapshot sharing the same physical pages.
// The page reference count is incremented for every shared page.
func (s *Snapshot) Fork() (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.authorized {
		return nil, ErrRevoked
	}

	pages := append([]PageID(nil), s.pages...)
	if err := s.pool.retain(pages); err != nil {
		return nil, err
	}
	return &Snapshot{
		pool:       s.pool,
		pageSize:   s.pageSize,
		size:       s.size,
		pages:      pages,
		authorized: true,
	}, nil
}

// PageIDs returns the snapshot's logical-to-physical page mapping. The caller
// owns the returned slice.
func (s *Snapshot) PageIDs() ([]PageID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.authorized {
		return nil, ErrRevoked
	}
	return append([]PageID(nil), s.pages...), nil
}

// PhysicalPage maps a zero-based logical page index to a physical page.
func (s *Snapshot) PhysicalPage(index int) (PageID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.authorized {
		return 0, ErrRevoked
	}
	if index < 0 || index >= len(s.pages) {
		return 0, ErrOutOfBounds
	}
	return s.pages[index], nil
}

// ReadAt copies bytes from the snapshot into p.
func (s *Snapshot) ReadAt(p []byte, off int64) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.authorized {
		return 0, ErrRevoked
	}
	if err := s.checkRange(off, len(p)); err != nil {
		return 0, err
	}

	written := 0
	for written < len(p) {
		position := off + int64(written)
		pageIndex := int(position / int64(s.pageSize))
		inPage := int(position % int64(s.pageSize))
		count := s.pageSize - inPage
		if count > len(p)-written {
			count = len(p) - written
		}
		if err := s.pool.readPage(s.pages[pageIndex], inPage, p[written:written+count]); err != nil {
			return written, err
		}
		written += count
	}
	return written, nil
}

// WriteAt performs copy-on-write for every touched shared page and then copies
// p into the snapshot's private pages.
func (s *Snapshot) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorized {
		return 0, ErrRevoked
	}
	if err := s.checkRange(off, len(p)); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}

	first := int(off / int64(s.pageSize))
	last := int((off + int64(len(p)) - 1) / int64(s.pageSize))
	ids := s.pages[first : last+1]
	replacements, err := s.pool.copyForWrite(ids)
	if err != nil {
		return 0, err
	}
	for i := first; i <= last; i++ {
		if replacement, ok := replacements[s.pages[i]]; ok {
			s.pages[i] = replacement
		}
	}

	written := 0
	for written < len(p) {
		position := off + int64(written)
		pageIndex := int(position / int64(s.pageSize))
		inPage := int(position % int64(s.pageSize))
		count := s.pageSize - inPage
		if count > len(p)-written {
			count = len(p) - written
		}
		if err := s.pool.writePage(s.pages[pageIndex], inPage, p[written:written+count]); err != nil {
			return written, err
		}
		written += count
	}
	return written, nil
}

func (s *Snapshot) checkRange(off int64, length int) error {
	if off < 0 || off > s.size || int64(length) > s.size-off {
		return fmt.Errorf("%w: offset=%d length=%d size=%d", ErrOutOfBounds, off, length, s.size)
	}
	return nil
}

// ReadAll returns a copy of the entire snapshot.
func (s *Snapshot) ReadAll() ([]byte, error) {
	buf := make([]byte, int(s.size))
	n, err := s.ReadAt(buf, 0)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// Bytes is an alias for ReadAll.
func (s *Snapshot) Bytes() ([]byte, error) {
	return s.ReadAll()
}

// Close revokes the snapshot and releases every page reference. Only the last
// reference to a page starts zeroization. Close is idempotent and returns the
// first release error on repeated calls.
func (s *Snapshot) Close() error {
	s.mu.Lock()
	if !s.authorized {
		released := s.released
		s.mu.Unlock()
		if released != nil {
			<-released
		}
		s.mu.RLock()
		err := s.releaseErr
		s.mu.RUnlock()
		return err
	}
	s.authorized = false
	ids := s.pages
	s.pages = nil
	s.released = make(chan struct{})
	s.mu.Unlock()

	err := s.pool.release(ids)

	s.mu.Lock()
	s.releaseErr = err
	close(s.released)
	s.mu.Unlock()
	return err
}

// Release is an alias for Close.
func (s *Snapshot) Release() error {
	return s.Close()
}

// Revoke is an alias for Close. The snapshot can no longer read, write or
// fork after revocation.
func (s *Snapshot) Revoke() error {
	return s.Close()
}

package bptree

import (
	"encoding/binary"
	"hash/crc32"
	"sort"
	"sync"
)

// Store is an in-memory page file: a flat byte array divided into fixed-size
// pages. Pages are addressed by PageID (slot index). A page, once written and
// published, is never modified in place by the tree (copy-on-write); the only
// mutating operations are Write to a freshly allocated page and Free during
// reclamation.
type Store struct {
	mu       sync.RWMutex
	data     []byte
	pageSize int
	npages   int
	freeList []PageID
	used     map[PageID]bool

	failWrites int // fault injection: number of upcoming Write calls that fail
}

// NewStore creates an empty page file with the given page size in bytes.
func NewStore(pageSize int) *Store {
	s := &Store{
		pageSize: pageSize,
		npages:   1, // slot 0 is the reserved null page
		used:     make(map[PageID]bool),
	}
	s.data = make([]byte, pageSize)
	return s
}

// PageSize returns the store page size in bytes.
func (s *Store) PageSize() int { return s.pageSize }

// Alloc reserves a page slot and returns its id. Reused slots come from the
// free list; otherwise the file grows by one page.
func (s *Store) Alloc() PageID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.freeList); n > 0 {
		id := s.freeList[n-1]
		s.freeList = s.freeList[:n-1]
		s.used[id] = true
		return id
	}
	id := PageID(s.npages)
	s.npages++
	s.data = append(s.data, make([]byte, s.pageSize)...)
	s.used[id] = true
	return id
}

// Write stores a full page image. It fails if the page is not allocated, the
// buffer size differs from the page size, or an injected failure is pending.
func (s *Store) Write(id PageID, buf []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWrites > 0 {
		s.failWrites--
		return ErrInjected
	}
	if !s.used[id] {
		return ErrNotAllocated
	}
	if len(buf) != s.pageSize {
		return ErrInvalidPageSize
	}
	copy(s.data[int(id)*s.pageSize:], buf)
	return nil
}

// Read returns a copy of an allocated page. It does not verify the page
// checksum; decoding (decodeNode) does.
func (s *Store) Read(id PageID) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.used[id] {
		return nil, ErrNotAllocated
	}
	buf := make([]byte, s.pageSize)
	copy(buf, s.data[int(id)*s.pageSize:])
	return buf, nil
}

// Free releases a page slot so it can be reused by a later Alloc. Only the
// reclamation pass calls Free, and only for pages unreachable from every
// active root.
func (s *Store) Free(id PageID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.used[id] {
		return ErrNotAllocated
	}
	delete(s.used, id)
	s.freeList = append(s.freeList, id)
	return nil
}

// Allocated returns the sorted ids of all currently allocated pages.
func (s *Store) Allocated() []PageID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]PageID, 0, len(s.used))
	for id := range s.used {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// AllocatedCount reports how many pages are currently allocated.
func (s *Store) AllocatedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.used)
}

// CheckIntegrity verifies magic bytes and CRC of every allocated page and
// returns the sorted ids of corrupt pages. It is the page-file corruption
// check and does not require a tree.
func (s *Store) CheckIntegrity() []PageID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var bad []PageID
	for id := range s.used {
		buf := s.data[int(id)*s.pageSize : (int(id)+1)*s.pageSize]
		if binary.LittleEndian.Uint32(buf[0:]) != pageMagic {
			bad = append(bad, id)
			continue
		}
		want := binary.LittleEndian.Uint32(buf[s.pageSize-crcSize:])
		got := crc32.Checksum(buf[:s.pageSize-crcSize], crc32c)
		if want != got {
			bad = append(bad, id)
		}
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i] < bad[j] })
	return bad
}

// SetWriteFailures arms fault injection: the next n Write calls fail with
// ErrInjected. Used to prove that a failed commit never publishes a new root.
func (s *Store) SetWriteFailures(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failWrites = n
}

// Corrupt flips one byte inside the payload of an allocated page, simulating
// page-file corruption. The page's CRC no longer matches afterwards.
func (s *Store) Corrupt(id PageID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.used[id] {
		return
	}
	off := int(id)*s.pageSize + headerSize
	s.data[off] ^= 0xFF
}

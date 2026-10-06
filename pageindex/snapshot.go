package pageindex

import (
	"errors"
	"sync"
)

// ErrSnapshotClosed is returned when reading a closed snapshot.
var ErrSnapshotClosed = errors.New("pageindex: snapshot closed")

// Snapshot pins one committed root version. The pages reachable from its
// root are guaranteed to stay allocated and unmodified until Close, no
// matter how the writer mutates the index afterwards.
type Snapshot struct {
	store   *Store
	root    PageID
	version uint64

	mu     sync.Mutex
	closed bool
}

// Snapshot captures the current root version.
func (st *Store) Snapshot() *Snapshot {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := &Snapshot{store: st, root: st.root, version: st.version}
	st.snaps[s] = struct{}{}
	return s
}

// RootPage returns the pinned root page number.
func (s *Snapshot) RootPage() PageID { return s.root }

// Version returns the pinned version.
func (s *Snapshot) Version() uint64 { return s.version }

// Get reads key from the pinned version. It returns ErrSnapshotClosed
// after Close. It never blocks the writer for longer than one lookup and
// never observes pages freed by reclamation: the store read lock is held
// across the closed check and the descent, so Close cannot recycle the
// pinned pages concurrently.
func (s *Snapshot) Get(key uint64) (uint64, error) {
	st := s.store
	st.mu.RLock()
	defer st.mu.RUnlock()

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, ErrSnapshotClosed
	}
	v, ok := st.getFrom(s.root, key)
	if !ok {
		return 0, ErrNotFound
	}
	return v, nil
}

// Close releases the pinned version. Pages reachable only from this
// snapshot become recyclable. Close is idempotent and safe to call
// concurrently from multiple goroutines.
func (s *Snapshot) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	st := s.store
	st.mu.Lock()
	delete(st.snaps, s)
	st.gcLocked()
	st.mu.Unlock()
}

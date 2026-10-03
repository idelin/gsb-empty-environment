package store

import (
	"errors"
	"sync"
	"syscall"
)

// Fault-injecting File implementations used to verify crash recovery and
// rollback under hostile conditions: sudden power loss, disk full, and
// torn writes. They are exported so integrators can reuse the harness.

// ErrCrashed is returned by every operation on a crashed SimFile until
// Reopen is called (the simulated machine is powered off).
var ErrCrashed = errors.New("simfs: machine is powered off")

// SimFile is an in-memory File that models storage durability honestly:
//
//   - WriteAt stages bytes in a volatile overlay (the OS page cache).
//   - Sync folds the overlay into the durable image (fsync).
//   - Crash simulates sudden power loss: the volatile overlay is discarded
//     and all further operations fail with ErrCrashed until Reopen.
//   - FullAfterBytes models disk full: writes that would push the durable
//     plus volatile image past the limit fail with syscall.ENOSPC, after
//     writing a torn prefix (real disks do partial writes too).
type SimFile struct {
	mu         sync.Mutex
	durable    []byte // what survives a power cut
	volatile   []byte // durable + staged writes (the "page cache" view)
	crashed    bool
	FullAfter  int64 // 0 = unlimited; beyond this, writes fail with ENOSPC
	TornWrites bool  // partial writes before ENOSPC (default true behavior)
}

// NewSimFile returns an empty simulated disk.
func NewSimFile() *SimFile { return &SimFile{TornWrites: true} }

func (s *SimFile) checkAlive() error {
	if s.crashed {
		return ErrCrashed
	}
	return nil
}

func (s *SimFile) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAlive(); err != nil {
		return 0, err
	}
	if off >= int64(len(s.volatile)) {
		return 0, nil
	}
	n := copy(p, s.volatile[off:])
	return n, nil
}

func (s *SimFile) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAlive(); err != nil {
		return 0, err
	}
	if s.FullAfter > 0 && off+int64(len(p)) > s.FullAfter {
		// Real disks often write a torn prefix before failing.
		if s.TornWrites {
			room := s.FullAfter - off
			if room > 0 {
				s.growLocked(off + room)
				copy(s.volatile[off:], p[:room])
			}
		}
		return 0, syscall.ENOSPC
	}
	s.growLocked(off + int64(len(p)))
	copy(s.volatile[off:], p)
	return len(p), nil
}

func (s *SimFile) growLocked(end int64) {
	if end <= int64(len(s.volatile)) {
		return
	}
	nv := make([]byte, end)
	copy(nv, s.volatile)
	s.volatile = nv
}

func (s *SimFile) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAlive(); err != nil {
		return err
	}
	nd := make([]byte, len(s.volatile))
	copy(nd, s.volatile)
	s.durable = nd
	return nil
}

func (s *SimFile) Truncate(size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAlive(); err != nil {
		return err
	}
	if size < int64(len(s.volatile)) {
		s.volatile = s.volatile[:size]
	}
	return nil
}

func (s *SimFile) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.volatile))
}

func (s *SimFile) Close() error { return nil }

// Crash cuts the power: staged (un-synced) writes are lost and the machine
// goes down. Durable bytes are exactly what the last Sync folded in,
// possibly narrowed by a Truncate rollback.
func (s *SimFile) Crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A truncate is a metadata operation; on real filesystems the size
	// change is typically journaled, so the durable image respects the
	// last rollback point. Model that: durable = min(durable, volatile).
	if len(s.durable) > len(s.volatile) {
		s.durable = s.durable[:len(s.volatile)]
	}
	s.volatile = nil
	s.crashed = true
}

// Reopen powers the machine back on. Only durable bytes remain.
func (s *SimFile) Reopen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volatile = make([]byte, len(s.durable))
	copy(s.volatile, s.durable)
	s.crashed = false
}

// DurableSize reports how many bytes would survive a power cut right now.
func (s *SimFile) DurableSize() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.durable))
}

// FlushPartial models the OS writeback daemon flushing the first n bytes of
// the dirty page cache to disk before a power cut: those bytes become
// durable even though the application never called Sync. n is clamped to
// the current volatile size.
func (s *SimFile) FlushPartial(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > int64(len(s.volatile)) {
		n = int64(len(s.volatile))
	}
	if n < int64(len(s.durable)) {
		return
	}
	nd := make([]byte, n)
	copy(nd, s.volatile[:n])
	s.durable = nd
}

// Package dbuf implements a versioned double-buffer store.
//
// A Store keeps at most two buffers: one active buffer serving readers and
// one spare buffer used to prepare the next version. Committing an update
// atomically swaps the prepared buffer into the active slot; the retired
// buffer is not reused until every reader holding it has released its view.
// If no spare buffer is available, BeginUpdate blocks until a reader drains
// or the context is cancelled. Aborting an update keeps the old version.
package dbuf

import (
	"context"
	"sync"
)

// buffer is one of the two physical buffers owned by a Store.
type buffer struct {
	version uint64
	data    []byte
	readers int
	retired bool // swapped out of the active slot, waiting for readers to drain
}

// Store is a versioned double buffer. The zero value is not usable; use New.
type Store struct {
	mu      sync.Mutex
	active  *buffer
	pending *buffer // checked out by an in-flight update
	idle    *buffer  // spare buffer ready for the next update
	changed chan struct{}
	version uint64
}

// New returns a Store whose active buffer holds the initial content as
// version 1.
func New(initial []byte) *Store {
	return &Store{
		active:  &buffer{version: 1, data: append([]byte(nil), initial...)},
		idle:    &buffer{},
		changed: make(chan struct{}),
		version: 1,
	}
}

// broadcastLocked wakes every goroutine waiting for a buffer.
func (s *Store) broadcastLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// View is a reader's handle on a published version. The underlying buffer is
// not reused until Release is called.
type View struct {
	store *Store
	buf   *buffer
	once  sync.Once
}

// Acquire returns a view of the current active version.
func (s *Store) Acquire() *View {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active.readers++
	return &View{store: s, buf: s.active}
}

// Version returns the version number of the viewed content.
func (v *View) Version() uint64 { return v.buf.version }

// Data returns the content marker published with this version. The slice is
// immutable and safe to read without synchronization.
func (v *View) Data() []byte { return v.buf.data }

// Release drops the view. It is idempotent. Releasing the last reader of a
// retired buffer returns it to the idle pool and unblocks waiting updaters.
func (v *View) Release() {
	v.once.Do(func() {
		s := v.store
		s.mu.Lock()
		defer s.mu.Unlock()
		v.buf.readers--
		if v.buf.retired && v.buf.readers == 0 {
			v.buf.retired = false
			v.buf.data = nil
			s.idle = v.buf
			s.broadcastLocked()
		}
	})
}

// Update is an in-flight write transaction on the spare buffer.
type Update struct {
	store *Store
	buf   *buffer
	done  bool
}

// BeginUpdate checks out the spare buffer for writing. It blocks while both
// buffers are busy (one active, one pending or retired with live readers)
// until a buffer is freed or ctx is cancelled.
func (s *Store) BeginUpdate(ctx context.Context) (*Update, error) {
	s.mu.Lock()
	for {
		if s.pending == nil && s.idle != nil {
			s.pending = s.idle
			s.idle = nil
			u := &Update{store: s, buf: s.pending}
			s.mu.Unlock()
			return u, nil
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
			s.mu.Lock()
		}
	}
}

// Commit publishes data as the next version and swaps the prepared buffer
// into the active slot. The retired buffer stays out of circulation until
// its remaining readers release it. Commit reports false (and changes
// nothing) if the update was already committed or aborted.
func (u *Update) Commit(data []byte) bool {
	s := u.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.done {
		return false
	}
	u.done = true

	s.version++
	u.buf.version = s.version
	u.buf.data = append([]byte(nil), data...)
	u.buf.readers = 0
	u.buf.retired = false

	old := s.active
	s.active = u.buf
	s.pending = nil

	if old.readers == 0 {
		s.idle = old
	} else {
		old.retired = true
	}
	s.broadcastLocked()
	return true
}

// Abort discards the prepared write, keeps the old active version, and
// returns the spare buffer to the idle pool. It is a no-op after Commit.
func (u *Update) Abort() {
	s := u.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.done {
		return
	}
	u.done = true
	s.pending = nil
	s.idle = u.buf
	s.broadcastLocked()
}

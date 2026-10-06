package doublebuf

import (
	"context"
	"errors"
	"sync"
)

// ErrUpdateClosed is returned when writing to or committing an Update that
// has already been committed or aborted.
var ErrUpdateClosed = errors.New("doublebuf: update already committed or aborted")

// buffer is one immutable-after-publish slot. data and version are only
// mutated while the buffer is the standby slot held by an Update, and are
// published by Commit flipping the active index under the Store lock.
type buffer struct {
	version uint64
	data    []byte
	refs    int  // readers currently pinning this buffer
	writing bool // an Update is preparing this buffer
}

// Store is a double-buffered, versioned store. The zero value is not
// usable; construct with New.
type Store struct {
	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every state change
	slots   [2]*buffer
	active  int    // index of the active (readable) slot
	version uint64 // version of the active buffer
}

// New returns a Store whose active buffer holds a copy of initial as
// version 1.
func New(initial []byte) *Store {
	return &Store{
		changed: make(chan struct{}),
		slots: [2]*buffer{
			{version: 1, data: clone(initial)},
			{},
		},
		version: 1,
	}
}

// Version returns the version of the currently active buffer.
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// signalLocked wakes every waiter blocked in Begin. Caller holds s.mu.
func (s *Store) signalLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// Acquire pins the active buffer and returns a View of it. The buffer is
// guaranteed not to be reused by writers until the View is released, so
// the View keeps observing a consistent snapshot even after newer
// versions are committed.
func (s *Store) Acquire() *View {
	s.mu.Lock()
	b := s.slots[s.active]
	b.refs++
	s.mu.Unlock()
	return &View{store: s, buf: b}
}

// View is a pinned read snapshot of one buffer.
type View struct {
	store *Store
	buf   *buffer
	once  sync.Once
}

// Version returns the version of the pinned buffer.
func (v *View) Version() uint64 { return v.buf.version }

// Bytes returns the content of the pinned buffer. It is valid until
// Release is called and must not be mutated.
func (v *View) Bytes() []byte { return v.buf.data }

// Release unpins the buffer. It is idempotent; the first call may unblock
// a writer waiting in Begin.
func (v *View) Release() {
	v.once.Do(func() {
		s := v.store
		s.mu.Lock()
		v.buf.refs--
		s.signalLocked()
		s.mu.Unlock()
	})
}

// Update prepares the standby buffer for the next commit. Exactly one of
// Commit or Abort must be called to release the standby slot.
type Update struct {
	store *Store
	buf   *buffer
	slot  int

	mu   sync.Mutex
	data []byte
	done bool
}

// Begin reserves the standby buffer for a new update. It blocks while the
// standby buffer is still pinned by readers or held by another update,
// and returns early with the context's error if ctx is canceled.
func (s *Store) Begin(ctx context.Context) (*Update, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	for {
		slot := 1 - s.active
		standby := s.slots[slot]
		if !standby.writing && standby.refs == 0 {
			standby.writing = true
			s.mu.Unlock()
			return &Update{store: s, buf: standby, slot: slot}, nil
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
			s.mu.Lock()
		}
	}
}

// Write stages p as the content for the next commit. The staged content
// is invisible to readers until Commit succeeds.
func (u *Update) Write(p []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.done {
		return ErrUpdateClosed
	}
	u.data = clone(p)
	return nil
}

// Commit atomically publishes the staged content as the new active buffer
// and returns its version. The previously active buffer is retired to the
// standby slot and stays readable by its pinned readers. Committing an
// already finished Update returns ErrUpdateClosed.
func (u *Update) Commit() (uint64, error) {
	u.mu.Lock()
	if u.done {
		u.mu.Unlock()
		return 0, ErrUpdateClosed
	}
	u.done = true
	data := u.data
	u.mu.Unlock()

	s := u.store
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	u.buf.version = s.version
	u.buf.data = data
	u.buf.writing = false
	s.active = u.slot
	s.signalLocked()
	return s.version, nil
}

// Abort discards the staged content and releases the standby slot. The
// active buffer keeps its old version untouched. Aborting an already
// finished Update returns ErrUpdateClosed.
func (u *Update) Abort() error {
	u.mu.Lock()
	if u.done {
		u.mu.Unlock()
		return ErrUpdateClosed
	}
	u.done = true
	u.mu.Unlock()

	s := u.store
	s.mu.Lock()
	u.buf.writing = false
	s.signalLocked()
	s.mu.Unlock()
	return nil
}

func clone(p []byte) []byte {
	if p == nil {
		return nil
	}
	q := make([]byte, len(p))
	copy(q, p)
	return q
}

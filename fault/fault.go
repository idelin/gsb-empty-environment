// Package fault wraps a seq.Store with scripted fault injection so tests can
// enumerate crash and failure scenarios: crash before the flush, crash after
// the flush, flush errors, and segment exhaustion under faults.
package fault

import (
	"errors"
	"sync"

	"seq"
)

// ErrCrash is returned when a simulated crash is injected. The wrapped store
// is left in exactly the state a real crash would have produced, so a fresh
// allocator built on the same inner store models a process restart.
var ErrCrash = errors.New("fault: simulated crash")

// ErrFlush is the default error returned for Fail* faults.
var ErrFlush = errors.New("fault: flush failed")

// Kind identifies the fault to inject.
type Kind int

const (
	// FailBeforeFlush returns an error without applying the write, modeling
	// a flush that failed before any durable write.
	FailBeforeFlush Kind = iota
	// FailAfterFlush applies the write but still returns an error, modeling
	// an ambiguous flush (e.g. response lost after the write landed).
	FailAfterFlush
	// CrashBeforeFlush returns ErrCrash without applying the write, modeling
	// a process crash before the segment was persisted.
	CrashBeforeFlush
	// CrashAfterFlush applies the write and then returns ErrCrash, modeling
	// a process crash after the segment was persisted but before it was used.
	CrashAfterFlush
)

// Fault injects a fault of the given Kind at the Nth (1-based)
// CompareAndSwap call. Err overrides the error returned for Fail* kinds.
type Fault struct {
	Call int
	Kind Kind
	Err  error
}

// Store is a seq.Store that injects scripted faults.
type Store struct {
	inner  seq.Store
	mu     sync.Mutex
	calls  int
	faults map[int][]Fault
}

// New wraps inner, injecting the given faults.
func New(inner seq.Store, faults ...Fault) *Store {
	m := make(map[int][]Fault)
	for _, f := range faults {
		m[f.Call] = append(m[f.Call], f)
	}
	return &Store{inner: inner, faults: m}
}

// Load implements seq.Store.
func (s *Store) Load() (seq.State, error) { return s.inner.Load() }

// CompareAndSwap implements seq.Store, injecting scripted faults.
func (s *Store) CompareAndSwap(expect, next seq.State) (bool, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	var f Fault
	inject := false
	if q := s.faults[call]; len(q) > 0 {
		f, q = q[0], q[1:]
		s.faults[call] = q
		inject = true
	}
	s.mu.Unlock()

	if !inject {
		return s.inner.CompareAndSwap(expect, next)
	}
	switch f.Kind {
	case FailBeforeFlush:
		return false, errOrDefault(f.Err)
	case CrashBeforeFlush:
		return false, ErrCrash
	case FailAfterFlush:
		ok, _ := s.inner.CompareAndSwap(expect, next)
		return ok, errOrDefault(f.Err)
	case CrashAfterFlush:
		ok, _ := s.inner.CompareAndSwap(expect, next)
		return ok, ErrCrash
	}
	return s.inner.CompareAndSwap(expect, next)
}

// Calls returns how many CompareAndSwap calls have been made.
func (s *Store) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func errOrDefault(err error) error {
	if err == nil {
		return ErrFlush
	}
	return err
}

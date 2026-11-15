package budget

import (
	"sync"
	"sync/atomic"
)

// faultySink is an injectable staging sink used to prove the engine survives
// hostile sinks. It can be configured to perform a short write, permanently
// return a fixed error, or recover after a short write and then behave.
type faultySink struct {
	mu       sync.Mutex
	buf      []byte
	released atomic.Bool

	// shortN > 0 makes the next Write report (shortN, nil). It is consumed once.
	shortN int
	// shortOnce, when true, applies shortN only to the first Write.
	shortOnce bool
	// zero, when true, makes Write report (0, nil): progress without error,
	// which the engine must surface as ErrShortWrite instead of looping.
	zero bool
	// faultErr, when non-nil, is returned by every Write.
	faultErr error
}

func (s *faultySink) Write(p []byte) (int, error) {
	if s.faultErr != nil {
		return 0, s.faultErr
	}
	if s.zero {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.shortN > 0 {
		n := s.shortN
		if n > len(p) {
			n = len(p)
		}
		s.buf = append(s.buf, p[:n]...)
		if s.shortOnce {
			s.shortN = 0
		}
		return n, nil
	}
	s.buf = append(s.buf, p...)
	return len(p), nil
}

func (s *faultySink) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf
}

func (s *faultySink) release() {
	s.released.Store(true)
	s.mu.Lock()
	s.buf = nil
	s.mu.Unlock()
}

// sinkTracker counts the sinks it created and how many were released, so
// tests can prove no buffer leaks on either the success or the failure path.
type sinkTracker struct {
	mu       sync.Mutex
	sinks    []*faultySink
	released int
}

func (t *sinkTracker) factory() func() sink {
	return func() sink {
		fs := &faultySink{}
		t.mu.Lock()
		t.sinks = append(t.sinks, fs)
		t.mu.Unlock()
		return &trackingSink{inner: fs, tracker: t}
	}
}

func (t *sinkTracker) allReleased() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.sinks {
		if !s.released.Load() {
			return false
		}
	}
	return true
}

func (t *sinkTracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sinks)
}

// trackingSink wraps faultySink and bumps the tracker on release.
type trackingSink struct {
	inner   *faultySink
	tracker *sinkTracker
}

func (t *trackingSink) Write(p []byte) (int, error) { return t.inner.Write(p) }
func (t *trackingSink) bytes() []byte               { return t.inner.bytes() }
func (t *trackingSink) release() {
	t.inner.release()
	t.tracker.mu.Lock()
	t.tracker.released++
	t.tracker.mu.Unlock()
}

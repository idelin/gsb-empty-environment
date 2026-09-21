package pool_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"resourcepool/pool"
)

// harness is a fake resource backend. Create timing and failures are injected
// by the test; the harness records how many resources currently exist.
type harness struct {
	cfgMu  sync.RWMutex
	newErr error
	delay  time.Duration
	gate   chan struct{} // if non-nil, each create blocks until this closes

	mu      sync.Mutex
	live    int
	peak    int
	created int
	closed  int
}

type fakeResource struct {
	id int
}

func (h *harness) setGate(g chan struct{}) {
	h.cfgMu.Lock()
	h.gate = g
	h.cfgMu.Unlock()
}

func (h *harness) setErr(err error) {
	h.cfgMu.Lock()
	h.newErr = err
	h.cfgMu.Unlock()
}

func (h *harness) config() (error, time.Duration, chan struct{}) {
	h.cfgMu.RLock()
	defer h.cfgMu.RUnlock()
	return h.newErr, h.delay, h.gate
}

func (h *harness) create(ctx context.Context) (any, error) {
	err, delay, gate := h.config()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.created++
	h.live++
	if h.live > h.peak {
		h.peak = h.live
	}
	return &fakeResource{id: h.created}, nil
}

func (h *harness) closeRes(v any) error {
	h.mu.Lock()
	h.live--
	h.closed++
	h.mu.Unlock()
	return nil
}

func (h *harness) snapshot() (live, peak, created, closed int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live, h.peak, h.created, h.closed
}

func newPoolWithHarness(t *testing.T, max int, h *harness) *pool.Pool {
	t.Helper()
	p := pool.New(pool.Config{
		MaxOpen: max,
		Create:  h.create,
		Close:   h.closeRes,
	})
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func mustAcquire(t *testing.T, p *pool.Pool, ctx context.Context) *pool.Handle {
	t.Helper()
	hd, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return hd
}

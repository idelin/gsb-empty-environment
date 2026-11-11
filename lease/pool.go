// Package lease implements generation-tagged, reference-counted segmented
// leases over pooled byte buffers.
//
// A binary protocol parser checks out one backing buffer per request, parses
// frames into sub-slices and lends those slices to asynchronous consumers.
// Every derived slice holds a shared reference on the lease: the backing
// buffer is neither zeroed nor returned to the pool while any reference is
// outstanding, and reads through a stale reference are rejected once the
// lease is terminated or the buffer is reused under a new generation.
package lease

import (
	"context"
	"sync"
	"sync/atomic"
)

// buffer is a single reusable backing array. Each checkout bumps gen so that
// references captured by an old tenant can detect reuse by a new tenant.
type buffer struct {
	id   int
	gen  uint64
	data []byte
}

// Pool hands out fixed-size backing buffers. It is deliberately tiny: a pool
// containing a single buffer is enough to prove that outstanding references
// block reuse.
type Pool struct {
	mu   sync.Mutex
	cond *sync.Cond
	free []*buffer

	// clears counts finalizations. Each lease finalizes exactly once, so the
	// counter equals the number of leases that have been fully released.
	clears atomic.Int64
}

// NewPool creates a pool holding size buffers, each with the given capacity.
func NewPool(size, capacity int) *Pool {
	if size <= 0 || capacity <= 0 {
		panic("lease: pool size and capacity must be positive")
	}
	p := &Pool{}
	p.cond = sync.NewCond(&p.mu)
	for i := 0; i < size; i++ {
		p.free = append(p.free, &buffer{id: i, data: make([]byte, capacity)})
	}
	return p
}

// Clears reports how many buffer zeroings have run on lease finalization.
func (p *Pool) Clears() int64 { return p.clears.Load() }

// Acquire waits until a buffer is free and checks it out under a fresh
// generation. It blocks while every buffer is pinned by outstanding
// references and returns ctx.Err() if the context fires first.
func (p *Pool) Acquire(ctx context.Context) (*Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for len(p.free) == 0 {
		if err := p.waitWhileEmpty(ctx); err != nil {
			return nil, err
		}
	}

	b := p.free[len(p.free)-1]
	p.free = p.free[:len(p.free)-1]
	b.gen++
	return newLease(p, b, b.gen), nil
}

// waitWhileEmpty blocks on the condition until a buffer is returned or ctx is
// canceled. A helper goroutine broadcasts the condition on cancellation.
func (p *Pool) waitWhileEmpty(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		case <-stop:
		}
	}()

	p.cond.Wait()
	close(stop)
	return ctx.Err()
}

// put returns a finalized buffer to the pool and wakes one blocked acquirer.
func (p *Pool) put(b *buffer) {
	p.mu.Lock()
	p.free = append(p.free, b)
	p.cond.Signal()
	p.mu.Unlock()
}

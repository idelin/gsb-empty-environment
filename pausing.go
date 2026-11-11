package netsec

import (
	"context"
	"sync"
)

// PausingEncryptor wraps another Encryptor and can block Encrypt calls for
// chosen key generations. It is the fault-injection primitive used to hold a
// background migration at a fixed point while a foreground write proceeds,
// proving the migration's compare-and-swap then loses and re-reads instead of
// overwriting the new data.
//
// Only Encrypt is ever paused; Decrypt always passes straight through so
// readers and the opening phase of a migration remain unblocked.
type PausingEncryptor struct {
	inner Encryptor

	mu       sync.Mutex
	cond     *sync.Cond
	blocked  map[KeyGen]bool
	oneShot  map[KeyGen]bool
	released chan struct{}
	waiters  map[KeyGen]int
}

// NewPausingEncryptor wraps inner.
func NewPausingEncryptor(inner Encryptor) *PausingEncryptor {
	p := &PausingEncryptor{
		inner:    inner,
		blocked:  make(map[KeyGen]bool),
		oneShot:  make(map[KeyGen]bool),
		released: make(chan struct{}),
		waiters:  make(map[KeyGen]int),
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Pause blocks all Encrypt calls for gen until Resume is called.
func (p *PausingEncryptor) Pause(gen KeyGen) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked[gen] = true
}

// PauseOnce blocks only the next Encrypt call for gen, then automatically
// clears the gate so subsequent calls proceed without another Resume. This
// targets the single sealing operation of one migration transaction.
func (p *PausingEncryptor) PauseOnce(gen KeyGen) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.oneShot[gen] = true
}

// Resume releases every Encrypt call currently blocked and removes all gates
// (including one-shot gates that have not yet fired).
func (p *PausingEncryptor) Resume() {
	p.mu.Lock()
	close(p.released)
	p.released = make(chan struct{})
	p.blocked = make(map[KeyGen]bool)
	p.oneShot = make(map[KeyGen]bool)
	p.mu.Unlock()
}

// BlockedCount reports how many Encrypt calls for gen are currently held.
func (p *PausingEncryptor) BlockedCount(gen KeyGen) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waiters[gen]
}

// WaitBlocked blocks until at least n Encrypt calls for gen are held. It is
// intended for tests that must synchronize on the stall before acting.
func (p *PausingEncryptor) WaitBlocked(ctx context.Context, gen KeyGen, n int) error {
	ctxDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
		close(ctxDone)
	}()

	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		count := p.waiters[gen]
		if count >= n {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.cond.Wait()
	}
}

func (p *PausingEncryptor) gateLocked(gen KeyGen) (chan struct{}, bool) {
	if p.blocked[gen] {
		return p.released, true
	}
	if p.oneShot[gen] {
		delete(p.oneShot, gen)
		return p.released, true
	}
	return nil, false
}

// Encrypt blocks while gen is gated, then delegates to the inner encryptor.
func (p *PausingEncryptor) Encrypt(ctx context.Context, gen KeyGen, plaintext []byte) ([]byte, error) {
	p.mu.Lock()
	release, hold := p.gateLocked(gen)
	if hold {
		p.waiters[gen]++
		p.cond.Broadcast()
	}
	p.mu.Unlock()

	if hold {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			p.waiters[gen]--
			p.cond.Broadcast()
			p.mu.Unlock()
			return nil, ctx.Err()
		case <-release:
		}
		p.mu.Lock()
		p.waiters[gen]--
		p.cond.Broadcast()
		p.mu.Unlock()
	}
	return p.inner.Encrypt(ctx, gen, plaintext)
}

// Decrypt always delegates without pausing.
func (p *PausingEncryptor) Decrypt(ctx context.Context, gen KeyGen, ciphertext []byte) ([]byte, error) {
	return p.inner.Decrypt(ctx, gen, ciphertext)
}

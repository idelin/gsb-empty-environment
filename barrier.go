package sortexec

import (
	"context"
	"sync"
)

// Barrier blocks Wait until Release is called or the context is done. It is
// meant to be installed as a Hook so tests can pause the executor at a phase
// boundary, cancel the context, and then let the executor observe the
// cancellation.
type Barrier struct {
	ch   chan struct{}
	once sync.Once
}

func NewBarrier() *Barrier { return &Barrier{ch: make(chan struct{})} }

// Wait blocks until the barrier is released or ctx is done, returning
// ctx.Err() in the latter case.
func (b *Barrier) Wait(ctx context.Context) error {
	select {
	case <-b.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release unblocks all current and future Wait calls. It is idempotent.
func (b *Barrier) Release() { b.once.Do(func() { close(b.ch) }) }

// Hook returns the barrier as a hook function suitable for Hooks fields.
func (b *Barrier) Hook() func(context.Context) error { return b.Wait }

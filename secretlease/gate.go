package secretlease

import (
	"context"
	"sync"
)

// Gate is a pause/resume control attached to a step. A step blocks inside
// [StepContext.Checkpoint] while its gate is paused, but remains interruptible
// by step-context cancellation.
type Gate struct {
	mu      sync.Mutex
	paused  bool
	blocked int
	resume  chan struct{}
}

// NewGate creates a running gate.
func NewGate() *Gate {
	return &Gate{resume: make(chan struct{})}
}

// Pause blocks future Checkpoint calls. Checkpoints already running stay
// blocked until Resume or step-context cancellation.
func (g *Gate) Pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		return
	}
	g.paused = true
	g.resume = make(chan struct{})
}

// Resume releases every checkpoint currently blocked on the gate.
func (g *Gate) Resume() {
	g.mu.Lock()
	if !g.paused {
		g.mu.Unlock()
		return
	}
	g.paused = false
	ch := g.resume
	g.mu.Unlock()

	close(ch)
}

// Blocked reports how many checkpoints are currently waiting.
func (g *Gate) Blocked() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blocked
}

// wait blocks until the gate is resumed or ctx is canceled. It returns
// immediately when the gate is not paused, and decrements the blocked counter
// on every exit path.
func (g *Gate) wait(ctx <-chan struct{}) error {
	g.mu.Lock()
	if !g.paused {
		g.mu.Unlock()
		return nil
	}
	ch := g.resume
	g.blocked++
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		g.blocked--
		g.mu.Unlock()
	}()

	select {
	case <-ch:
		return nil
	case <-ctx:
		return context.Canceled
	}
}

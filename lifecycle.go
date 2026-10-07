// Package lifecycle provides a phased shutdown coordinator for background
// work. A Lifecycle moves through four phases: it receives tasks, drains
// them (waiting for in-flight tasks and any subtasks they register while
// draining), releases resources via deferred cleanups, and stops.
package lifecycle

import (
	"context"
	"errors"
	"sync"
)

// Phase describes where the lifecycle currently is.
type Phase int

const (
	// Receiving accepts new top-level work.
	Receiving Phase = iota
	// Draining still accepts subtasks from in-flight tasks; shutdown
	// waits for all of them to finish.
	Draining
	// Releasing runs deferred cleanups; no new work is accepted.
	Releasing
	// Stopped is terminal; the shutdown result is final and shared.
	Stopped
)

func (p Phase) String() string {
	switch p {
	case Receiving:
		return "Receiving"
	case Draining:
		return "Draining"
	case Releasing:
		return "Releasing"
	case Stopped:
		return "Stopped"
	}
	return "Unknown"
}

// ErrNotAccepting is returned by Go and Defer once the lifecycle has
// entered the Releasing phase (or is already Stopped).
var ErrNotAccepting = errors.New("lifecycle: not accepting new work")

// Lifecycle coordinates the phased shutdown of a set of goroutines.
type Lifecycle struct {
	mu       sync.Mutex
	phase    Phase
	pending  int
	drained  bool
	drainCh  chan struct{}
	cleanups []func()
	firstErr error

	stopCh chan struct{}
	result error
}

// New returns a Lifecycle in the Receiving phase.
func New() *Lifecycle {
	return &Lifecycle{
		phase:   Receiving,
		drainCh: make(chan struct{}),
		stopCh:  make(chan struct{}),
	}
}

// Phase reports the current phase.
func (l *Lifecycle) Phase() Phase {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.phase
}

// Done returns a channel that is closed once the lifecycle is Stopped.
func (l *Lifecycle) Done() <-chan struct{} { return l.stopCh }

// Go registers fn as tracked work and runs it in its own goroutine.
//
// Work may be registered while Receiving or Draining, so an in-flight
// task may spawn subtasks during shutdown and drain will wait for them.
// A task must register its subtasks before it returns. Once the
// lifecycle reaches Releasing, Go returns ErrNotAccepting.
func (l *Lifecycle) Go(fn func() error) error {
	l.mu.Lock()
	if l.phase >= Releasing {
		l.mu.Unlock()
		return ErrNotAccepting
	}
	l.pending++
	l.mu.Unlock()

	go func() {
		err := fn()
		l.mu.Lock()
		if err != nil && l.firstErr == nil {
			l.firstErr = err
		}
		l.pending--
		if l.pending == 0 && l.phase >= Draining {
			l.signalDrainedLocked()
		}
		l.mu.Unlock()
	}()
	return nil
}

// Defer registers fn to run during the Releasing phase, in LIFO order,
// whether shutdown succeeds or a task failed. It returns ErrNotAccepting
// once Releasing has begun.
func (l *Lifecycle) Defer(fn func()) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.phase >= Releasing {
		return ErrNotAccepting
	}
	l.cleanups = append(l.cleanups, fn)
	return nil
}

// Shutdown moves the lifecycle from Receiving to Draining and waits for
// every received task and its registered subtasks to finish, then runs
// deferred cleanups (Releasing) and settles in Stopped.
//
// The shutdown result — the first task error, or nil — is computed once
// and shared by every Shutdown call. If ctx expires first, Shutdown
// returns ctx.Err(), but the drain continues in the background and the
// lifecycle still reaches Stopped; later calls observe the shared result.
func (l *Lifecycle) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	if l.phase == Receiving {
		l.phase = Draining
		if l.pending == 0 {
			l.signalDrainedLocked()
		}
		go l.finish()
	}
	l.mu.Unlock()

	select {
	case <-l.stopCh:
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.result
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Wait shuts the lifecycle down, blocks until it is Stopped, and returns
// the shared shutdown result.
func (l *Lifecycle) Wait() error { return l.Shutdown(context.Background()) }

func (l *Lifecycle) signalDrainedLocked() {
	if !l.drained {
		l.drained = true
		close(l.drainCh)
	}
}

// finish runs in the background once draining starts: it waits for the
// pending count to reach zero, releases resources, and stops. It runs
// independently of any Shutdown caller's context.
func (l *Lifecycle) finish() {
	<-l.drainCh

	l.mu.Lock()
	l.phase = Releasing
	cleanups := l.cleanups
	l.mu.Unlock()

	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}

	l.mu.Lock()
	l.phase = Stopped
	l.result = l.firstErr
	l.mu.Unlock()
	close(l.stopCh)
}

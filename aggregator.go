package netsec

import (
	"sync"
)

// Aggregator accumulates detector receipts for one object and publishes a
// decision once the whole object can be judged. It is safe for concurrent use
// by gateway goroutines.
type Aggregator struct {
	mu        sync.Mutex
	machine   *machine
	scheduler Scheduler
	cancel    func()
	final     chan Decision
	closed    bool
}

// Option configures an Aggregator.
type Option func(*Aggregator)

// WithScheduler injects a custom scheduler, primarily a FakeScheduler in tests.
func WithScheduler(scheduler Scheduler) Option {
	return func(aggregator *Aggregator) {
		aggregator.scheduler = scheduler
	}
}

// NewAggregator creates an aggregator backed by wall-clock timers.
func NewAggregator(options ...Option) *Aggregator {
	aggregator := &Aggregator{
		machine:   newMachine(),
		scheduler: newWallScheduler(),
		final:     make(chan Decision, 1),
	}
	for _, option := range options {
		option(aggregator)
	}
	return aggregator
}

// InstallManifest installs the manifest for a generation. A newer generation
// supersedes the previous one (for example after a fragment replacement) and
// discards its outstanding timer. The same generation with identical content
// is a no-op. Obsolete or conflicting manifests return an error.
func (a *Aggregator) InstallManifest(manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	outcome := a.machine.applyManifest(manifest)
	switch outcome {
	case installRejectedObsolete:
		return ErrObsoleteManifest
	case installRejectedConflict:
		return ErrConflictingManifest
	case installRedundant:
		return nil
	}

	a.armTimerLocked()
	return nil
}

// Submit applies one detector receipt. The boolean is false when the receipt
// was ignored: bound to a stale/superseded generation, an unknown chunk/check,
// or delivered after the decision became terminal (including repeated
// receipts). Malformed receipts return an error and never touch state.
func (a *Aggregator) Submit(receipt Receipt) (bool, error) {
	if err := receipt.Validate(); err != nil {
		return false, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	applied := a.machine.applyReceipt(receipt)
	a.finishIfTerminalLocked()
	return applied, nil
}

// Cancel marks the current generation incomplete. Cancellations that target a
// superseded generation or arrive after the terminal decision are ignored.
func (a *Aggregator) Cancel(generation int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	applied := a.machine.applyCancel(generation)
	a.finishIfTerminalLocked()
	return applied
}

// Decision returns a point-in-time snapshot. While receipts are outstanding
// the object is Incomplete and non-terminal; only a terminal snapshot can be
// Pass or Blocked (or Incomplete due to timeout, failure or cancellation).
func (a *Aggregator) Decision() Decision {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.machine.decision()
}

// Generation returns the installed generation, or -1 before any manifest.
func (a *Aggregator) Generation() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.machine.generation()
}

// Stats returns a copy of the event counters.
func (a *Aggregator) Stats() Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.machine.stats
}

// Final returns a channel that receives the decision exactly once, when the
// object becomes terminal. The channel is buffered so the publish never
// blocks detector goroutines.
func (a *Aggregator) Final() <-chan Decision {
	return a.final
}

// armTimerLocked schedules the current generation's timeout and replaces any
// timer inherited from a superseded generation.
func (a *Aggregator) armTimerLocked() {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	timeout := a.machine.current.manifest.Timeout
	if timeout <= 0 || a.machine.terminal() {
		return
	}
	generation := a.machine.generation()
	a.cancel = a.scheduler.After(timeout, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.machine.applyTimeout(generation)
		a.finishIfTerminalLocked()
	})
}

// finishIfTerminalLocked stops the timer and publishes the terminal decision
// once. The buffered channel makes publishing safe while holding the mutex.
func (a *Aggregator) finishIfTerminalLocked() {
	if !a.machine.terminal() || a.closed {
		return
	}
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.closed = true
	a.final <- a.machine.decision()
}

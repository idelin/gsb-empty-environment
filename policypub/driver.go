package policypub

import (
	"context"
	"sync"
)

// Step identifies an individual step of a publication driven by Pausable.
type Step string

const (
	// StepBeforeInstall fires before an outbound SA is installed.
	StepBeforeInstallOut Step = "before-install-outbound"
	// StepAfterInstall fires after an outbound SA is installed.
	StepAfterInstallOut Step = "after-install-outbound"
	// StepBeforeInstallIn fires before an inbound SA is installed.
	StepBeforeInstallIn Step = "before-install-inbound"
	// StepAfterInstallIn fires after an inbound SA is installed.
	StepAfterInstallIn Step = "after-install-inbound"
	// StepBeforeSwap fires after all installs, before the atomic swap.
	StepBeforeSwap Step = "before-swap"
	// StepUninstall fires on every uninstall, including rollback and the
	// retirement of a previous generation.
	StepUninstall Step = "uninstall"
)

// Event is one recorded driver action, for test assertions and auditing.
type Event struct {
	Step       Step
	Handle     string
	Direction  Direction
	Generation uint64
}

// gate lets a test pause the driver at a specific step.
type gate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	arrived  bool
	released bool
}

func newGate() *gate {
	g := &gate{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// hold blocks until the gate is released. Multiple goroutines may wait.
func (g *gate) hold() {
	g.mu.Lock()
	g.arrived = true
	g.cond.Broadcast()
	for !g.released {
		g.cond.Wait()
	}
	g.mu.Unlock()
}

// waitArrival blocks until some goroutine reaches the gate.
func (g *gate) waitArrival() {
	g.mu.Lock()
	for !g.arrived {
		g.cond.Wait()
	}
	g.mu.Unlock()
}

// release unblocks every waiter.
func (g *gate) release() {
	g.mu.Lock()
	g.released = true
	g.cond.Broadcast()
	g.mu.Unlock()
}

// Pausable wraps an Installer and gives a verification harness precise
// control over publication progress: per-step pause/release, install failure
// injection, and an ordered event log. It is intended for testing and for
// safety verification harnesses rather than production hot paths.
type Pausable struct {
	inner Installer

	mu     sync.Mutex
	gates  map[Step]*gate
	fail   map[string]error
	events []Event
}

// NewPausable wraps the given installer with pause and fault-injection hooks.
func NewPausable(inner Installer) *Pausable {
	if inner == nil {
		panic("policypub: wrapped installer is nil")
	}
	return &Pausable{
		inner: inner,
		gates: make(map[Step]*gate),
		fail:  make(map[string]error),
	}
}

// Hold arms a fresh gate on step. Any driver reaching that step blocks until
// Release is called. Must be called before the publication starts.
func (p *Pausable) Hold(step Step) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gates[step] = newGate()
}

// WaitHeld blocks until a driver goroutine has reached the held step.
func (p *Pausable) WaitHeld(step Step) {
	p.mu.Lock()
	g, ok := p.gates[step]
	p.mu.Unlock()
	if ok {
		g.waitArrival()
	}
}

// Release lets any driver blocked at step continue. Releasing an unheld step
// is a no-op.
func (p *Pausable) Release(step Step) {
	p.mu.Lock()
	g, ok := p.gates[step]
	delete(p.gates, step)
	p.mu.Unlock()
	if ok {
		g.release()
	}
}

// Fail makes the next install of handle return err without touching the
// underlying installer. The fault is one-shot.
func (p *Pausable) Fail(handle string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[handle] = err
}

func (p *Pausable) waitGate(step Step) {
	p.mu.Lock()
	g, ok := p.gates[step]
	p.mu.Unlock()
	if ok {
		g.hold()
	}
}

func (p *Pausable) record(ev Event) {
	p.mu.Lock()
	p.events = append(p.events, ev)
	p.mu.Unlock()
}

// Events returns a copy of the ordered event log.
func (p *Pausable) Events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Event(nil), p.events...)
}

// Reset clears gates, faults and the event log.
func (p *Pausable) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gates = make(map[Step]*gate)
	p.fail = make(map[string]error)
	p.events = nil
}

// Install implements Installer, applying failure injection and step gates.
func (p *Pausable) Install(ctx context.Context, sa SA) error {
	before := StepBeforeInstallOut
	after := StepAfterInstallOut
	if sa.Direction == Inbound {
		before = StepBeforeInstallIn
		after = StepAfterInstallIn
	}

	p.record(Event{Step: before, Handle: sa.Handle, Direction: sa.Direction})
	p.waitGate(before)

	p.mu.Lock()
	err, failSet := p.fail[sa.Handle]
	if failSet {
		delete(p.fail, sa.Handle)
	}
	p.mu.Unlock()
	if failSet {
		return err
	}
	if err := p.inner.Install(ctx, sa); err != nil {
		return err
	}

	p.record(Event{Step: after, Handle: sa.Handle, Direction: sa.Direction})
	p.waitGate(after)
	return nil
}

// Uninstall implements Installer.
func (p *Pausable) Uninstall(ctx context.Context, handle string) error {
	p.record(Event{Step: StepUninstall, Handle: handle})
	p.waitGate(StepUninstall)
	return p.inner.Uninstall(ctx, handle)
}

// Installed delegates to the wrapped installer.
func (p *Pausable) Installed(handle string) bool {
	return p.inner.Installed(handle)
}

// BeforeSwap implements SwapBarrier so the publisher blocks at the swap point
// when a gate is installed on StepBeforeSwap.
func (p *Pausable) BeforeSwap(generation uint64) {
	p.record(Event{Step: StepBeforeSwap, Generation: generation})
	p.waitGate(StepBeforeSwap)
}

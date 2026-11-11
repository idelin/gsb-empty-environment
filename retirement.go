package netsec

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// Proof can only be established when every known thread has reported at a
// safepoint against the same barrier generation and neither its executing
// instruction range nor any return address on its stack references the page
// being retired. Without that proof the page must stay read-only executable.
var (
	ErrNoBarrier        = errors.New("netsec: no retirement barrier is open")
	ErrBarrierOpen      = errors.New("netsec: a retirement barrier is already open")
	ErrInvalidInterval  = errors.New("netsec: invalid target interval")
	ErrUnknownThread    = errors.New("netsec: report from unregistered thread")
	ErrStaleGeneration  = errors.New("netsec: report generation does not match the open barrier")
	ErrThreadExists     = errors.New("netsec: thread is already registered")
	ErrProofIncomplete  = errors.New("netsec: proof incomplete: not every thread has reported this generation")
	ErrPageReferenced   = errors.New("netsec: proof rejected: a live thread still references the target page")
	ErrNoParticipants   = errors.New("netsec: proof impossible: no threads are registered")
	ErrProtectionFailed = errors.New("netsec: page could not be made writable")
)

// PageProtector abstracts the host memory-protection operation.
//
// Production hosts wire it to mprotect(RW); tests use a fake so the
// retirement protocol itself can be exercised without mapping memory.
type PageProtector interface {
	// MakeWritable downgrades the target page from read-only executable to
	// read-write (and non-executable). It is invoked only after a proof has
	// been established.
	MakeWritable(target Interval) error
}

// Report is the evidence a thread produces once it reaches a safepoint.
//
// InstructionRange is the range the thread is executing inside; ReturnAddrs
// is the set of return addresses found on its stack. Both are checked
// against the retired page because a thread may not be executing in the page
// at this instant yet still return into it after unwinding.
type Report struct {
	ThreadID         uint64
	Generation       uint64
	InstructionRange Interval
	ReturnAddrs      []uintptr
}

func (r Report) references(page Interval) bool {
	if page.Overlaps(r.InstructionRange) {
		return true
	}
	for _, addr := range r.ReturnAddrs {
		if page.Contains(addr) {
			return true
		}
	}
	return false
}

type threadState struct {
	id            uint64
	lastReportGen uint64
	latest        Report
	hasReport     bool
}

type barrier struct {
	generation uint64
	target     Interval
}

// Coordinator drives code-page retirement for a Go host.
//
// The protocol is:
//
//  1. Host threads register on startup and call ReportAtSafepoint whenever
//     the runtime parks them at a safepoint.
//  2. To recycle a code page the host calls BeginRetirement, which opens a
//     numbered barrier and records the target page. Threads that register
//     while the barrier is open join the current generation as participants.
//  3. Each thread reports its instruction range and return addresses tagged
//     with Coordinator.Generation().
//  4. WaitRetire (or repeated TryRetire) only downgrades the page once every
//     registered thread has a same-generation, non-referencing report.
//  5. A failed attempt leaves the barrier open; CancelRetirement aborts it.
type Coordinator struct {
	mu        sync.Mutex
	protector PageProtector
	waiters   map[*sync.Cond]struct{}
	threads   map[uint64]*threadState
	gen       uint64
	open      *barrier
	lastErr   error
}

// NewCoordinator builds a Coordinator using protector for the actual
// permission change. protector may be nil for protocol-only simulations.
func NewCoordinator(protector PageProtector) *Coordinator {
	c := &Coordinator{
		protector: protector,
		waiters:   make(map[*sync.Cond]struct{}),
		threads:   make(map[uint64]*threadState),
	}
	return c
}

// broadcastLocked wakes every active waiter. Each waiting call owns a
// private sync.Cond so that a watchdog left over from an expired context can
// never signal an unrelated, later wait.
func (c *Coordinator) broadcastLocked() {
	for cond := range c.waiters {
		cond.Broadcast()
	}
}

// RegisterThread enrolls a host thread as a barrier participant.
//
// If a retirement barrier is already open the new thread joins the current
// generation and must itself report before a proof can be established; this
// prevents a freshly spawned thread from sneaking into the old page.
func (c *Coordinator) RegisterThread(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.threads[id]; exists {
		return ErrThreadExists
	}
	c.threads[id] = &threadState{id: id}
	return nil
}

// UnregisterThread removes a thread. The host must only call this once the
// thread has actually stopped and can never jump into generated code again.
// If a barrier is open the thread stops counting as a missing participant.
func (c *Coordinator) UnregisterThread(id uint64) {
	c.mu.Lock()
	delete(c.threads, id)
	c.broadcastLocked()
	c.mu.Unlock()
}

// BeginRetirement opens a new barrier for target and returns its generation.
// All existing and subsequently registered threads must report against this
// generation before the page may become writable.
func (c *Coordinator) BeginRetirement(target Interval) (uint64, error) {
	if !target.Valid() {
		return 0, ErrInvalidInterval
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open != nil {
		return 0, ErrBarrierOpen
	}
	c.gen++
	c.open = &barrier{generation: c.gen, target: target}
	c.lastErr = nil
	return c.gen, nil
}

// CancelRetirement aborts the open barrier. The page remains read-only
// executable and the generation is not advanced.
func (c *Coordinator) CancelRetirement() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		return ErrNoBarrier
	}
	c.open = nil
	c.lastErr = nil
	c.broadcastLocked()
	return nil
}

// Generation returns the open barrier generation, or zero when no barrier
// is open. Threads read it when building a safepoint report.
func (c *Coordinator) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		return 0
	}
	return c.open.generation
}

// ReportAtSafepoint records the evidence gathered when a thread is parked at
// a safepoint. Reports tagged with a generation that is no longer open, or
// that differs from the open barrier, are rejected rather than silently
// treated as fresh proof.
func (c *Coordinator) ReportAtSafepoint(rep Report) error {
	if !rep.InstructionRange.Valid() {
		return ErrInvalidInterval
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		return ErrNoBarrier
	}
	thread, ok := c.threads[rep.ThreadID]
	if !ok {
		return ErrUnknownThread
	}
	if rep.Generation != c.open.generation {
		return ErrStaleGeneration
	}
	thread.latest = rep
	thread.lastReportGen = rep.Generation
	thread.hasReport = true
	c.broadcastLocked()
	return nil
}

// ProofStatus is a point-in-time view of an open barrier, useful for
// diagnostics and for asserting safety in tests.
type ProofStatus struct {
	Open             bool
	Generation       uint64
	Target           Interval
	ParticipantCount int
	ReportedCount    int
	StalledThreads   []uint64
	ReferencingIDs   []uint64
	Complete         bool
}

// Snapshot returns the current proof status without changing any state.
func (c *Coordinator) Snapshot() ProofStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := ProofStatus{Open: c.open != nil}
	if c.open == nil {
		return status
	}
	status.Generation = c.open.generation
	status.Target = c.open.target
	status.ParticipantCount = len(c.threads)
	for id, thread := range c.threads {
		if !thread.hasReport || thread.lastReportGen != c.open.generation {
			status.StalledThreads = append(status.StalledThreads, id)
			continue
		}
		status.ReportedCount++
		if thread.latest.references(c.open.target) {
			status.ReferencingIDs = append(status.ReferencingIDs, id)
		}
	}
	slices.Sort(status.StalledThreads)
	slices.Sort(status.ReferencingIDs)
	status.Complete = status.Open &&
		status.ParticipantCount > 0 &&
		len(status.StalledThreads) == 0 &&
		len(status.ReferencingIDs) == 0
	return status
}

// TryRetire performs a single non-blocking proof attempt against the open
// barrier. On success the page is made writable and the barrier closes.
// On failure the barrier stays open and the page stays read-only executable.
func (c *Coordinator) TryRetire() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tryRetireLocked()
}

func (c *Coordinator) tryRetireLocked() error {
	if c.open == nil {
		return ErrNoBarrier
	}
	if len(c.threads) == 0 {
		c.lastErr = ErrNoParticipants
		return ErrNoParticipants
	}
	target := c.open.target
	for _, thread := range c.threads {
		if !thread.hasReport || thread.lastReportGen != c.open.generation {
			c.lastErr = ErrProofIncomplete
			return ErrProofIncomplete
		}
		if thread.latest.references(target) {
			c.lastErr = ErrPageReferenced
			return ErrPageReferenced
		}
	}
	if c.protector != nil {
		if err := c.protector.MakeWritable(target); err != nil {
			c.lastErr = errors.Join(ErrProtectionFailed, err)
			return c.lastErr
		}
	}
	c.open = nil
	c.lastErr = nil
	c.broadcastLocked()
	return nil
}

// LastError returns the reason the most recent failed attempt gave.
func (c *Coordinator) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// WaitRetire blocks until a proof is established and the page is made
// writable, the barrier is cancelled, or ctx ends. It re-evaluates whenever
// a report, registration or unregistration happens, so it never spins.
func (c *Coordinator) WaitRetire(ctx context.Context) error {
	return c.waitSignal(ctx, func() (bool, error) {
		if c.open == nil {
			return true, ErrNoBarrier
		}
		if c.proofReadyLocked() {
			return true, c.tryRetireLocked()
		}
		return false, nil
	})
}

func (c *Coordinator) proofReadyLocked() bool {
	if c.open == nil || len(c.threads) == 0 {
		return false
	}
	for _, thread := range c.threads {
		if !thread.hasReport || thread.lastReportGen != c.open.generation {
			return false
		}
		if thread.latest.references(c.open.target) {
			return false
		}
	}
	return true
}

// Retire opens a barrier and waits up to timeout for a proof. A non-positive
// timeout waits forever. It is the high-level entry point for Go hosts.
func (c *Coordinator) Retire(target Interval, timeout time.Duration) error {
	if _, err := c.BeginRetirement(target); err != nil {
		return err
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	return c.WaitRetire(ctx)
}

// waitSignal runs check under the coordinator lock. check returns finished
// true to stop waiting (with err nil on success). Waiting is interrupted when
// ctx ends or any state change broadcasts. The wait uses a private Cond
// registered for its lifetime, so a watchdog from an already-expired context
// can only wake its own waiter and never a later, unrelated wait.
func (c *Coordinator) waitSignal(ctx context.Context, check func() (bool, error)) error {
	cond := sync.NewCond(&c.mu)
	stop := make(chan struct{})
	watchdogOnce := sync.Once{}
	stopWatchdog := func() {
		watchdogOnce.Do(func() { close(stop) })
	}
	go func() {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			cond.Broadcast()
			c.mu.Unlock()
		case <-stop:
		}
	}()

	c.mu.Lock()
	defer func() {
		delete(c.waiters, cond)
		stopWatchdog()
		c.mu.Unlock()
	}()
	c.waiters[cond] = struct{}{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		finished, err := check()
		if finished {
			return err
		}
		cond.Wait()
	}
}

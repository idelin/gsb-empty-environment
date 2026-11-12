package netsec

import (
	"errors"
	"sync"
)

// PID is a process identifier. Numeric PIDs are reusable: once a process
// exits its PID may be handed out to a different tenant.
type PID int

// TID is a thread identifier within a process. TIDs are reusable too.
type TID int

// Gen uniquely identifies an incarnation of a process. Every Spawn produces a
// fresh, monotonically increasing generation, even when the numeric PID is
// reused.
type Gen int64

// Regs models the register set of a stopped thread.
type Regs struct {
	PC   uint64
	SP   uint64
	Arg0 uint64
}

// ThreadState is the run/stop state of a model thread.
type ThreadState int

const (
	ThreadRunning ThreadState = iota
	ThreadStopped
	ThreadExited
)

// StopEventKind enumerates the model-kernel events observed by a tracer.
type StopEventKind int

const (
	// EventStop is delivered when a thread enters a stopped state.
	EventStop StopEventKind = iota
	// EventClone is delivered when a process creates a new thread.
	EventClone
	// EventExit is delivered when the whole process has terminated.
	EventExit
	// EventThreadExit is delivered when a single thread terminates.
	EventThreadExit
)

// Event is a single debugging event delivered to a tracer's queue.
type Event struct {
	Kind      StopEventKind
	PID       PID
	Gen       Gen
	TID       TID
	ThreadGen Gen
}

// Outcome describes who ended up owning a stop on a thread.
type Outcome int

const (
	// SuspendedByMe means the caller's request drove the thread to stop and
	// the caller is responsible for resuming it.
	SuspendedByMe Outcome = iota
	// StoppedByOther means the thread was already stopped by another tracer;
	// the caller only observes the stop and must not resume it.
	StoppedByOther
	// ThreadGone means the thread (or its process) no longer exists.
	ThreadGone
)

var (
	// ErrUnknownProcess is returned for PIDs with no live incarnation.
	ErrUnknownProcess = errors.New("netsec: unknown process")
	// ErrUnknownThread is returned for TIDs with no live thread.
	ErrUnknownThread = errors.New("netsec: unknown thread")
	// ErrNoAuthorization is returned when a tracer has not been authorized
	// for the target generation.
	ErrNoAuthorization = errors.New("netsec: missing debug authorization")
	// ErrAuthStale is returned when an authorization belongs to a previous
	// incarnation of a reused PID.
	ErrAuthStale = errors.New("netsec: debug authorization is for a stale generation")
	// ErrNotStopped is returned when register access is attempted on a
	// running thread.
	ErrNotStopped = errors.New("netsec: thread is not stopped")
	// ErrNotOwner is returned when a tracer resumes a thread it does not own.
	ErrNotOwner = errors.New("netsec: thread stop is owned by another tracer")
)

// Auth is an unforgeable, generation-bound debugging authorization.
type Auth struct {
	id     int
	pid    PID
	gen    Gen
	tracer int
}

func (a Auth) valid() bool { return a.id != 0 }

type tracer struct {
	id     int
	events []Event
}

type thread struct {
	tid       TID
	threadID  Gen
	state     ThreadState
	stoppedBy int // tracer id that owns the stop; 0 when running
	regs      Regs
}

type process struct {
	pid     PID
	gen     Gen
	alive   bool
	threads map[TID]*thread
	auths   map[int]Auth
	nextTID TID
}

// Kernel is a deterministic in-memory model of the small slice of an OS
// debugging interface (ptrace-like) that an attach transaction depends on.
type Kernel struct {
	mu sync.Mutex

	nextGen      Gen
	nextThreadID Gen
	nextTracer   int
	nextAuth     int
	freePIDs     []PID
	freeTIDs     map[PID][]TID
	processes    map[PID]*process
	tracers      map[int]*tracer
}

// NewKernel creates an empty model kernel.
func NewKernel() *Kernel {
	return &Kernel{
		nextGen:      1,
		nextThreadID: 1,
		nextTracer:   1,
		nextAuth:     1,
		freeTIDs:     make(map[PID][]TID),
		processes:    make(map[PID]*process),
		tracers:      make(map[int]*tracer),
	}
}

// Spawn creates a new process with one thread. It reuses a recycled numeric
// PID when one is available but always assigns a fresh generation.
func (k *Kernel) Spawn() (PID, Gen) {
	k.mu.Lock()
	defer k.mu.Unlock()

	pid := k.allocPIDLocked()
	gen := k.nextGen
	k.nextGen++

	tid := TID(1)
	threadID := k.nextThreadID
	k.nextThreadID++
	proc := &process{
		pid:     pid,
		gen:     gen,
		alive:   true,
		nextTID: 2,
		threads: map[TID]*thread{
			tid: {tid: tid, threadID: threadID, state: ThreadRunning, regs: Regs{PC: 1, SP: 2, Arg0: 3}},
		},
		auths: make(map[int]Auth),
	}
	k.processes[pid] = proc
	return pid, gen
}

// OpenPid resolves a numeric PID to the currently live incarnation. It returns
// ErrUnknownProcess if no live process currently holds the PID.
func (k *Kernel) OpenPid(pid PID) (*ProcHandle, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	proc, ok := k.processes[pid]
	if !ok || !proc.alive {
		return nil, ErrUnknownProcess
	}
	return &ProcHandle{kernel: k, pid: pid, gen: proc.gen}, nil
}

// ProcHandle is a reference to a specific process incarnation. Even if the PID
// is later reused, Alive reports false because the bound generation is gone.
type ProcHandle struct {
	kernel *Kernel
	pid    PID
	gen    Gen
}

// PID returns the numeric process id.
func (h *ProcHandle) PID() PID { return h.pid }

// Gen returns the bound generation.
func (h *ProcHandle) Gen() Gen { return h.gen }

// Alive reports whether the exact incarnation the handle refers to still runs.
func (h *ProcHandle) Alive() bool {
	k := h.kernel
	k.mu.Lock()
	defer k.mu.Unlock()
	proc, ok := k.processes[h.pid]
	return ok && proc.alive && proc.gen == h.gen
}

// NewTracer registers a debugging client and returns its opaque id.
func (k *Kernel) NewTracer() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	id := k.nextTracer
	k.nextTracer++
	k.tracers[id] = &tracer{id: id}
	return id
}

// Authorize grants a tracer debugging rights over a specific incarnation of a
// process. The authorization cannot be used against a later tenant reusing the
// same numeric PID.
func (k *Kernel) Authorize(tracerID int, h *ProcHandle) (Auth, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if _, ok := k.tracers[tracerID]; !ok {
		return Auth{}, ErrNoAuthorization
	}
	proc, ok := k.processes[h.pid]
	if !ok || !proc.alive || proc.gen != h.gen {
		return Auth{}, ErrUnknownProcess
	}
	auth := Auth{id: k.nextAuth, pid: h.pid, gen: h.gen, tracer: tracerID}
	k.nextAuth++
	proc.auths[auth.id] = auth
	return auth, nil
}

// ReleaseAuth revokes a previously granted authorization.
func (k *Kernel) ReleaseAuth(auth Auth) {
	k.mu.Lock()
	defer k.mu.Unlock()
	proc, ok := k.processes[auth.pid]
	if !ok {
		return
	}
	delete(proc.auths, auth.id)
}

func (k *Kernel) checkAuthLocked(auth Auth, proc *process) error {
	if !auth.valid() || auth.tracer == 0 {
		return ErrNoAuthorization
	}
	if _, ok := proc.auths[auth.id]; !ok || auth.gen != proc.gen {
		// A reused PID means the authorization binds to a dead generation.
		return ErrAuthStale
	}
	return nil
}

// RequestSuspend asks the kernel to stop a thread. It returns whether the
// caller now owns the stop, merely observes a stop owned by another tracer, or
// the thread has vanished.
func (k *Kernel) RequestSuspend(auth Auth, tid TID) (Outcome, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	proc, th, err := k.resolveLocked(auth, tid)
	if err != nil {
		return ThreadGone, err
	}
	switch th.state {
	case ThreadExited:
		return ThreadGone, nil
	case ThreadStopped:
		if th.stoppedBy == auth.tracer {
			return SuspendedByMe, nil
		}
		// Another tracer owns the stop. The caller is still an authorized
		// same-generation observer, so it receives a stop confirmation to
		// satisfy its barrier, but does not own the stop and cannot resume.
		k.enqueueLocked(proc, Event{
			Kind:      EventStop,
			PID:       proc.pid,
			Gen:       proc.gen,
			TID:       th.tid,
			ThreadGen: th.threadID,
		})
		return StoppedByOther, nil
	default:
		th.state = ThreadStopped
		th.stoppedBy = auth.tracer
		k.enqueueLocked(proc, Event{
			Kind:      EventStop,
			PID:       proc.pid,
			Gen:       proc.gen,
			TID:       th.tid,
			ThreadGen: th.threadID,
		})
		return SuspendedByMe, nil
	}
}

// Resume releases a stop. Only the tracer that owns the stop may resume it;
// a tracer that only observed someone else's stop gets ErrNotOwner.
func (k *Kernel) Resume(auth Auth, tid TID) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	_, th, err := k.resolveLocked(auth, tid)
	if err != nil {
		return err
	}
	if th.state == ThreadExited {
		return ErrUnknownThread
	}
	if th.state == ThreadRunning {
		return nil
	}
	if th.stoppedBy != auth.tracer {
		return ErrNotOwner
	}
	th.state = ThreadRunning
	th.stoppedBy = 0
	return nil
}

// ReadRegisters returns the registers of a stopped thread. The thread must be
// stopped, the authorization must match the live generation, and the caller's
// tracer must be the one currently holding the stop.
func (k *Kernel) ReadRegisters(auth Auth, tid TID) (Regs, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	_, th, err := k.resolveLocked(auth, tid)
	if err != nil {
		return Regs{}, err
	}
	if th.state == ThreadExited {
		return Regs{}, ErrUnknownThread
	}
	if th.state != ThreadStopped {
		return Regs{}, ErrNotStopped
	}
	// Any tracer with a valid same-generation authorization may read a
	// stopped thread. Resume, by contrast, stays restricted to the owner.
	return th.regs, nil
}

// StartThread models the target creating a new thread (clone). The new thread
// is announced to every authorized tracer via an EventClone and starts running.
func (k *Kernel) StartThread(h *ProcHandle) (TID, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	proc, ok := k.processes[h.pid]
	if !ok || !proc.alive || proc.gen != h.gen {
		return 0, ErrUnknownProcess
	}
	tid := k.allocateTIDLocked(proc)
	threadID := k.nextThreadID
	k.nextThreadID++
	proc.threads[tid] = &thread{
		tid:      tid,
		threadID: threadID,
		state:    ThreadRunning,
		regs:     Regs{PC: 101, SP: 102, Arg0: 103},
	}
	k.enqueueLocked(proc, Event{
		Kind:      EventClone,
		PID:       h.pid,
		Gen:       h.gen,
		TID:       tid,
		ThreadGen: threadID,
	})
	return tid, nil
}

// ExitThread models one thread terminating while the process stays alive.
func (k *Kernel) ExitThread(h *ProcHandle, tid TID) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	proc, ok := k.processes[h.pid]
	if !ok || !proc.alive || proc.gen != h.gen {
		return ErrUnknownProcess
	}
	th, ok := proc.threads[tid]
	if !ok {
		return ErrUnknownThread
	}
	threadID := th.threadID
	th.state = ThreadExited
	th.stoppedBy = 0
	k.enqueueLocked(proc, Event{
		Kind:      EventThreadExit,
		PID:       h.pid,
		Gen:       h.gen,
		TID:       tid,
		ThreadGen: threadID,
	})
	k.recycleTIDLocked(h.pid, tid)
	return nil
}

// KillProcess terminates an incarnation, delivers EventExit to its tracers and
// makes the numeric PID available for reuse.
func (k *Kernel) KillProcess(h *ProcHandle) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	proc, ok := k.processes[h.pid]
	if !ok || !proc.alive || proc.gen != h.gen {
		return ErrUnknownProcess
	}
	proc.alive = false
	k.enqueueLocked(proc, Event{Kind: EventExit, PID: proc.pid, Gen: proc.gen})

	for tid := range proc.threads {
		k.recycleTIDLocked(proc.pid, tid)
	}
	delete(k.processes, proc.pid)
	k.freePIDs = append(k.freePIDs, proc.pid)
	return nil
}

// DrainEvents returns and clears the queued events for a tracer.
func (k *Kernel) DrainEvents(tracerID int) []Event {
	k.mu.Lock()
	defer k.mu.Unlock()
	t, ok := k.tracers[tracerID]
	if !ok {
		return nil
	}
	out := t.events
	t.events = nil
	return out
}

// ThreadState reports the current state of a thread, used by tests.
func (k *Kernel) ThreadState(h *ProcHandle, tid TID) (ThreadState, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	proc, ok := k.processes[h.pid]
	if !ok || !proc.alive || proc.gen != h.gen {
		return ThreadExited, ErrUnknownProcess
	}
	th, ok := proc.threads[tid]
	if !ok {
		return ThreadExited, ErrUnknownThread
	}
	return th.state, nil
}

// SetRegs overwrites a thread's registers, used by tests to mark tenants.
func (k *Kernel) SetRegs(h *ProcHandle, tid TID, regs Regs) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	proc, ok := k.processes[h.pid]
	if !ok || !proc.alive || proc.gen != h.gen {
		return ErrUnknownProcess
	}
	th, ok := proc.threads[tid]
	if !ok {
		return ErrUnknownThread
	}
	th.regs = regs
	return nil
}

func (k *Kernel) resolveLocked(auth Auth, tid TID) (*process, *thread, error) {
	proc, ok := k.processes[auth.pid]
	if !ok || !proc.alive || proc.gen != auth.gen {
		return nil, nil, ErrAuthStale
	}
	if err := k.checkAuthLocked(auth, proc); err != nil {
		return nil, nil, err
	}
	th, ok := proc.threads[tid]
	if !ok || th.state == ThreadExited {
		return proc, nil, ErrUnknownThread
	}
	return proc, th, nil
}

func (k *Kernel) enqueueLocked(proc *process, ev Event) {
	for id := range proc.auths {
		auth := proc.auths[id]
		tracer, ok := k.tracers[auth.tracer]
		if !ok {
			continue
		}
		tracer.events = append(tracer.events, ev)
	}
}

func (k *Kernel) allocPIDLocked() PID {
	if n := len(k.freePIDs); n > 0 {
		pid := k.freePIDs[n-1]
		k.freePIDs = k.freePIDs[:n-1]
		return pid
	}
	return PID(len(k.processes) + len(k.freePIDs) + 1)
}

// allocTIDLocked reuses a TID that belonged to an exited thread in this
// incarnation, otherwise hands out the next monotonic one.
func (k *Kernel) allocateTIDLocked(proc *process) TID {
	if free := k.freeTIDs[proc.pid]; len(free) > 0 {
		tid := free[len(free)-1]
		k.freeTIDs[proc.pid] = free[:len(free)-1]
		return tid
	}
	tid := proc.nextTID
	proc.nextTID++
	return tid
}

func (k *Kernel) recycleTIDLocked(pid PID, tid TID) {
	proc, ok := k.processes[pid]
	if ok {
		delete(proc.threads, tid)
	}
	k.freeTIDs[pid] = append(k.freeTIDs[pid], tid)
}

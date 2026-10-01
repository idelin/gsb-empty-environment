// Package fiber implements a deterministic, cooperative, lightweight
// coroutine (fiber) scheduler.
//
// The scheduler is single-threaded and single-process. Fibers run one at a
// time and only give up control explicitly via Yield (or by returning).
// Scheduling follows a round-robin policy over the set of runnable fibers,
// and time is a virtual discrete clock advanced by a fixed time slice on
// every scheduling step. Because there is exactly one active fiber at any
// moment and the ready-queue order is fully deterministic, the produced
// event/tick sequence is bit-for-bit reproducible across runs.
package fiber

import (
	"fmt"
	"sync"
)

// State describes the lifecycle state of a Fiber.
type State int

const (
	// Ready means the fiber is waiting in the run queue.
	Ready State = iota
	// Running means the fiber currently holds the (virtual) CPU.
	Running
	// Suspended means the fiber yielded and waits to be resumed.
	Suspended
	// Dead means the fiber's function returned; it will never run again.
	Dead
)

func (s State) String() string {
	switch s {
	case Ready:
		return "Ready"
	case Running:
		return "Running"
	case Suspended:
		return "Suspended"
	case Dead:
		return "Dead"
	}
	return "Unknown"
}

// EventKind identifies the type of a scheduling event.
type EventKind int

const (
	// EventSpawn is emitted when a fiber is admitted to the scheduler.
	EventSpawn EventKind = iota
	// EventResume is emitted when the scheduler dispatches a fiber.
	EventResume
	// EventYield is emitted when a fiber voluntarily suspends itself.
	EventYield
	// EventExit is emitted when a fiber's function returns.
	EventExit
)

func (k EventKind) String() string {
	switch k {
	case EventSpawn:
		return "SPAWN"
	case EventResume:
		return "RESUME"
	case EventYield:
		return "YIELD"
	case EventExit:
		return "EXIT"
	}
	return "UNKNOWN"
}

// Event is a single entry of the deterministic scheduling log.
// Tick is the virtual clock value in microseconds at which the event
// occurred. The virtual clock advances by exactly SliceMicros per
// scheduling step, so the tick sequence is reproducible run after run.
type Event struct {
	Tick  uint64
	Kind  EventKind
	Fiber int
}

func (e Event) String() string {
	return fmt.Sprintf("tick=%06dus event=%-6s fiber=%d", e.Tick, e.Kind, e.Fiber)
}

// signal is the message a fiber sends back to the scheduler when it
// relinquishes control.
type signal struct {
	exited bool
}

// Fiber is a lightweight cooperative coroutine. The zero value is not
// usable; create fibers with Scheduler.Spawn.
type Fiber struct {
	id    int
	name  string
	state State

	sched  *Scheduler
	fn     func(f *Fiber)
	resume chan struct{}
	yield  chan signal
}

// ID returns the fiber's unique identifier within its scheduler.
func (f *Fiber) ID() int { return f.id }

// Name returns the fiber's human-readable name.
func (f *Fiber) Name() string { return f.name }

// State returns the fiber's current lifecycle state.
func (f *Fiber) State() State { return f.state }

// Yield suspends the calling fiber and deterministically hands control
// back to the scheduler, which will dispatch the next runnable fiber in
// round-robin order. It must only be called from within the fiber's own
// function while the fiber is running.
func (f *Fiber) Yield() {
	if f.state != Running {
		panic("fiber: Yield called on a non-running fiber")
	}
	f.sched.emit(Event{Tick: f.sched.clock, Kind: EventYield, Fiber: f.id})
	f.state = Suspended
	f.yield <- signal{exited: false}
	<-f.resume
}

// Scheduler is a deterministic single-threaded round-robin fiber
// scheduler with a virtual discrete-event clock.
type Scheduler struct {
	// SliceMicros is the length of one scheduling time slice in virtual
	// microseconds. The virtual clock advances by this amount on every
	// dispatch. Defaults to 1 if zero.
	SliceMicros uint64

	fibers []*Fiber
	clock  uint64
	log    []Event

	started bool
	wg      sync.WaitGroup
}

// Spawn creates a new fiber running fn and admits it to the run queue.
// Spawn must be called before Run. The fiber does not start executing
// until the scheduler dispatches it.
func (s *Scheduler) Spawn(name string, fn func(f *Fiber)) *Fiber {
	if s.started {
		panic("fiber: Spawn called after Run")
	}
	f := &Fiber{
		id:     len(s.fibers),
		name:   name,
		state:  Ready,
		sched:  s,
		fn:     fn,
		resume: make(chan struct{}),
		yield:  make(chan signal),
	}
	s.fibers = append(s.fibers, f)
	s.emit(Event{Tick: s.clock, Kind: EventSpawn, Fiber: f.id})
	return f
}

// Log returns a copy of the deterministic scheduling log recorded so far.
func (s *Scheduler) Log() []Event {
	out := make([]Event, len(s.log))
	copy(out, s.log)
	return out
}

// Clock returns the current virtual clock value in microseconds.
func (s *Scheduler) Clock() uint64 { return s.clock }

// emit appends an event to the scheduling log.
func (s *Scheduler) emit(e Event) {
	s.log = append(s.log, e)
}

// run is the trampoline executed by each fiber's goroutine. It parks
// until the scheduler dispatches the fiber for the first time, then runs
// the fiber function and reports its exit.
func (f *Fiber) run() {
	<-f.resume
	f.fn(f)
	f.state = Dead
	f.sched.emit(Event{Tick: f.sched.clock, Kind: EventExit, Fiber: f.id})
	f.yield <- signal{exited: true}
}

// Run executes the scheduler until every fiber has exited. It advances
// the virtual clock by SliceMicros on each dispatch and selects the next
// runnable fiber in strict round-robin order, making the whole execution
// (and its event log) fully deterministic and reproducible.
func (s *Scheduler) Run() {
	if s.started {
		panic("fiber: Run called twice")
	}
	s.started = true
	if s.SliceMicros == 0 {
		s.SliceMicros = 1
	}

	s.wg.Add(len(s.fibers))
	for _, f := range s.fibers {
		go func(f *Fiber) {
			defer s.wg.Done()
			f.run()
		}(f)
	}

	alive := len(s.fibers)
	cursor := 0
	for alive > 0 {
		f := s.fibers[cursor]
		cursor = (cursor + 1) % len(s.fibers)
		if f.state == Dead {
			continue
		}
		// Dispatch f for exactly one time slice.
		s.clock += s.SliceMicros
		f.state = Running
		s.emit(Event{Tick: s.clock, Kind: EventResume, Fiber: f.id})
		f.resume <- struct{}{}
		sig := <-f.yield
		if sig.exited {
			alive--
		}
	}

	s.wg.Wait()
}

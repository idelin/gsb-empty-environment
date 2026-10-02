// Package fiber implements a deterministic, single-threaded, cooperative
// coroutine (fiber) scheduler with round-robin dispatch.
//
// Determinism model:
//   - Exactly one fiber runs at any moment; the scheduler goroutine itself
//     is the only other active goroutine.
//   - Hand-off between scheduler and fiber uses unbuffered channels, so a
//     fiber runs only after the scheduler explicitly resumes it, and the
//     scheduler proceeds only after the fiber yields or exits.
//   - Dispatch order is a FIFO ready queue (round-robin). No timers, no
//     randomness, no preemption: the event sequence is a pure function of
//     the program, so every run produces a byte-identical tick log.
package fiber

import (
	"fmt"
	"strings"
)

// EventKind classifies scheduler log entries.
type EventKind int

const (
	EventSpawn  EventKind = iota // fiber enqueued for the first time
	EventResume                  // scheduler resumed a fiber
	EventYield                   // fiber yielded back to the scheduler
	EventExit                    // fiber returned and left the scheduler
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

// Event is one entry in the deterministic scheduler log.
type Event struct {
	Tick    uint64 // logical clock; increments by exactly 1 per event
	FiberID int
	Name    string
	Kind    EventKind
}

func (e Event) String() string {
	return fmt.Sprintf("tick=%04d fiber=%d(%s) event=%s", e.Tick, e.FiberID, e.Name, e.Kind)
}

// signal is sent from a fiber back to the scheduler at a hand-off point.
type signal int

const (
	sigYield signal = iota
	sigExit
)

// Fiber is a cooperative coroutine. Its function receives the *Fiber and
// calls Yield to voluntarily return control to the scheduler.
type Fiber struct {
	ID     int
	Name   string
	fn     func(*Fiber)
	sched  *Scheduler
	resume chan struct{} // scheduler -> fiber: "you may run now"
}

// Yield suspends the calling fiber and appends it to the back of the
// scheduler's ready queue. It blocks until the scheduler resumes it.
func (f *Fiber) Yield() {
	f.sched.handoff <- sigYield
	<-f.resume
}

// Scheduler is a single-threaded round-robin fiber scheduler.
type Scheduler struct {
	ready   []*Fiber // FIFO ready queue
	log     []Event
	tick    uint64
	handoff chan signal // fiber -> scheduler: yield/exit signal
	nextID  int
}

// New returns an empty scheduler.
func New() *Scheduler {
	return &Scheduler{handoff: make(chan signal)}
}

// Spawn creates a fiber and enqueues it. The fiber does not run until Run
// is called. Spawn must be called before Run (or from a running fiber).
func (s *Scheduler) Spawn(name string, fn func(*Fiber)) {
	f := &Fiber{
		ID:     s.nextID,
		Name:   name,
		fn:     fn,
		sched:  s,
		resume: make(chan struct{}),
	}
	s.nextID++
	s.ready = append(s.ready, f)
	s.record(f, EventSpawn)
	// The fiber goroutine is created exactly once and parks on f.resume
	// before executing any user code, so f.resume never has more than
	// one waiter and every hand-off stays deterministic.
	go s.runFiber(f)
}

// Tick returns the current logical clock value.
func (s *Scheduler) Tick() uint64 { return s.tick }

// Log returns the recorded event sequence.
func (s *Scheduler) Log() []Event { return append([]Event(nil), s.log...) }

// LogString renders the log as one "tick=... fiber=... event=..." line per event.
func (s *Scheduler) LogString() string {
	var b strings.Builder
	for _, e := range s.log {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	return b.String()
}

func (s *Scheduler) record(f *Fiber, kind EventKind) {
	s.log = append(s.log, Event{Tick: s.tick, FiberID: f.ID, Name: f.Name, Kind: kind})
	s.tick++
}

// Run dispatches fibers round-robin until every fiber has exited.
// It blocks the calling goroutine and returns the final tick count.
func (s *Scheduler) Run() uint64 {
	for len(s.ready) > 0 {
		f := s.ready[0]
		s.ready = s.ready[1:]

		s.record(f, EventResume)
		f.resume <- struct{}{}

		switch <-s.handoff {
		case sigYield:
			s.record(f, EventYield)
			s.ready = append(s.ready, f) // back of the queue: round-robin
		case sigExit:
			s.record(f, EventExit)
		}
	}
	return s.tick
}

// runFiber executes the fiber body. It parks on f.resume before the first
// instruction of fn, so the scheduler keeps full control of interleaving.
func (s *Scheduler) runFiber(f *Fiber) {
	<-f.resume
	f.fn(f)
	s.handoff <- sigExit
}

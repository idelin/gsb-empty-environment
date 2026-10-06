// Package desim implements a discrete-event simulation engine.
//
// Every event is ordered by a Key of (time, phase, insertion sequence).
// Events scheduled at the same time as the engine's current position may
// not be inserted into an earlier, already-executed phase: such inserts
// are explicitly rejected with a *PhaseError.
//
// Events can be cancelled while pending (cancelling an executed event
// fails) and can spawn causal successors from inside their handler.
//
// The engine also supports checkpoints and causal undo: undoing an
// executed event undoes its whole causal successor chain and replays
// from the nearest checkpoint. Externally confirmed effects are never
// undone; they produce compensation requirements instead.
package desim

import (
	"fmt"
	"sort"
)

// Key is the total order of events: time, then phase, then insertion
// sequence number.
type Key struct {
	Time  int64
	Phase int
	Seq   uint64
}

// Less reports whether k orders before o.
func (k Key) Less(o Key) bool {
	if k.Time != o.Time {
		return k.Time < o.Time
	}
	if k.Phase != o.Phase {
		return k.Phase < o.Phase
	}
	return k.Seq < o.Seq
}

func (k Key) String() string {
	return fmt.Sprintf("(t=%d,phase=%d,seq=%d)", k.Time, k.Phase, k.Seq)
}

// EventID identifies an event. The zero value is reserved (root cause).
type EventID uint64

// Handler is the body of an event. It runs when the event is executed
// and may schedule causal successors through the Context.
type Handler func(ctx *Context)

// Option customizes an event at schedule time.
type Option func(*Event)

// External marks an event whose effect has been externally confirmed.
// Such effects are never undone; undoing them yields a compensation
// requirement instead.
func External() Option {
	return func(ev *Event) { ev.External = true }
}

// Event is a scheduled or executed simulation event.
type Event struct {
	ID       EventID
	Key      Key
	Name     string
	Cause    EventID // 0 for root events
	External bool

	handler   Handler
	executed  bool
	execIdx   int
	cancelled bool
	undone    bool
	compens   bool
}

// Executed reports whether the event has executed.
func (ev *Event) Executed() bool { return ev.executed }

// Cancelled reports whether the event was cancelled while pending.
func (ev *Event) Cancelled() bool { return ev.cancelled }

// Undone reports whether the event's effects have been undone.
func (ev *Event) Undone() bool { return ev.undone }

// ExecIndex is the event's position in the execution history (-1 if
// it never executed).
func (ev *Event) ExecIndex() int {
	if !ev.executed {
		return -1
	}
	return ev.execIdx
}

// PhaseError is returned when an event is rejected because it would be
// inserted at a time in the past, or at the current time into a phase
// that has already been executed.
type PhaseError struct {
	Now      int64
	CurPhase int
	At       int64
	Phase    int
}

func (e *PhaseError) Error() string {
	return fmt.Sprintf("desim: rejected insert at (time=%d, phase=%d): engine already passed (time=%d, phase=%d); cannot insert into an already-executed phase",
		e.At, e.Phase, e.Now, e.CurPhase)
}

// State is the user state mutated by handlers. Snapshot must return a
// deep copy; Restore must deep-copy the snapshot back in. The engine
// uses it for checkpoints, rollback and from-scratch resimulation.
type State interface {
	Snapshot() interface{}
	Restore(snap interface{})
}

// Context is handed to a Handler while its event executes.
type Context struct {
	eng   *Engine
	event *Event
}

// Now is the engine's current simulation time.
func (c *Context) Now() int64 { return c.eng.now }

// Event is the event currently executing.
func (c *Context) Event() *Event { return c.event }

// Schedule inserts a causal successor of the current event, delay time
// units after now. During replay (after an undo) scheduling is
// suppressed and (0, nil) is returned.
func (c *Context) Schedule(name string, delay int64, phase int, h Handler, opts ...Option) (EventID, error) {
	return c.ScheduleAt(c.eng.now+delay, name, phase, h, opts...)
}

// ScheduleAt inserts a causal successor of the current event at an
// absolute time. During replay scheduling is suppressed.
func (c *Context) ScheduleAt(at int64, name string, phase int, h Handler, opts ...Option) (EventID, error) {
	if c.eng.replaying {
		return 0, nil
	}
	return c.eng.schedule(c.event.ID, name, at, phase, h, opts)
}

// Engine is the discrete-event scheduler. Its pending queue is an
// ordered list kept sorted by Key.
type Engine struct {
	state State

	now     int64
	phase   int
	started bool
	seq     uint64
	nextID  EventID

	queue    []*Event // ordered list, sorted by Key
	events   map[EventID]*Event
	children map[EventID][]EventID
	history  []*Event

	checkpoints   []checkpoint
	compensations []Compensation
	replayLog     []string

	executing bool
	replaying bool
}

// New creates an engine over the given user state. state may be nil, in
// which case checkpoints, undo and resimulation are unavailable.
func New(state State) *Engine {
	e := &Engine{
		state:    state,
		nextID:   1,
		events:   map[EventID]*Event{},
		children: map[EventID][]EventID{},
	}
	if state != nil {
		// genesis checkpoint: enables rollback to the very beginning.
		e.checkpoints = append(e.checkpoints, checkpoint{0, state.Snapshot()})
	}
	return e
}

// Now is the engine's current simulation time.
func (e *Engine) Now() int64 { return e.now }

// Phase is the phase of the last event executed at the current time.
func (e *Engine) Phase() int { return e.phase }

// Pending is the number of events waiting in the queue.
func (e *Engine) Pending() int { return len(e.queue) }

// Schedule inserts a root event (no causal parent) at an absolute time
// and phase. It fails with *PhaseError if (at, phase) lies at or before
// an already-executed position.
func (e *Engine) Schedule(name string, at int64, phase int, h Handler, opts ...Option) (EventID, error) {
	return e.schedule(0, name, at, phase, h, opts)
}

func (e *Engine) schedule(cause EventID, name string, at int64, phase int, h Handler, opts []Option) (EventID, error) {
	if h == nil {
		return 0, fmt.Errorf("desim: nil handler for event %q", name)
	}
	if e.started && (at < e.now || (at == e.now && phase < e.phase)) {
		return 0, &PhaseError{Now: e.now, CurPhase: e.phase, At: at, Phase: phase}
	}
	ev := &Event{
		ID:      e.nextID,
		Key:     Key{Time: at, Phase: phase, Seq: e.seq},
		Name:    name,
		Cause:   cause,
		handler: h,
	}
	e.nextID++
	e.seq++
	for _, o := range opts {
		o(ev)
	}
	e.events[ev.ID] = ev
	if cause != 0 {
		e.children[cause] = append(e.children[cause], ev.ID)
	}
	e.insert(ev)
	return ev.ID, nil
}

// insert keeps the queue an ordered list sorted by Key.
func (e *Engine) insert(ev *Event) {
	i := sort.Search(len(e.queue), func(i int) bool { return ev.Key.Less(e.queue[i].Key) })
	e.queue = append(e.queue, nil)
	copy(e.queue[i+1:], e.queue[i:])
	e.queue[i] = ev
}

func (e *Engine) removeFromQueue(id EventID) bool {
	for i, ev := range e.queue {
		if ev.ID == id {
			e.queue = append(e.queue[:i], e.queue[i+1:]...)
			return true
		}
	}
	return false
}

// Cancel removes a pending event. Cancelling an event that has already
// been executed fails.
func (e *Engine) Cancel(id EventID) error {
	if e.replaying {
		return fmt.Errorf("desim: cancel during replay not allowed")
	}
	ev, ok := e.events[id]
	if !ok {
		return fmt.Errorf("desim: cancel: unknown event %d", id)
	}
	if ev.executed {
		return fmt.Errorf("desim: cancel: event %d (%q) already executed", id, ev.Name)
	}
	if ev.cancelled {
		return fmt.Errorf("desim: cancel: event %d (%q) already cancelled", id, ev.Name)
	}
	ev.cancelled = true
	e.removeFromQueue(id)
	return nil
}

// Step executes the next event in key order. It returns false when the
// queue is empty.
func (e *Engine) Step() bool {
	if len(e.queue) == 0 {
		return false
	}
	ev := e.queue[0]
	e.queue = e.queue[1:]
	e.now, e.phase, e.started = ev.Key.Time, ev.Key.Phase, true
	ev.executed = true
	ev.execIdx = len(e.history)
	e.history = append(e.history, ev)
	e.executing = true
	ev.handler(&Context{eng: e, event: ev})
	e.executing = false
	return true
}

// Run executes events until the queue is empty.
func (e *Engine) Run() {
	for e.Step() {
	}
}

// RunUntil executes events up to and including time t.
func (e *Engine) RunUntil(t int64) {
	for len(e.queue) > 0 && e.queue[0].Key.Time <= t {
		e.Step()
	}
}

// History returns the executed events in execution order.
func (e *Engine) History() []*Event {
	out := make([]*Event, len(e.history))
	copy(out, e.history)
	return out
}

// Event looks up an event by ID.
func (e *Engine) Event(id EventID) *Event { return e.events[id] }

// Compensations lists the compensation requirements produced by undoing
// externally confirmed effects.
func (e *Engine) Compensations() []Compensation {
	out := make([]Compensation, len(e.compensations))
	copy(out, e.compensations)
	return out
}

// ReplayLog returns the replay log of the most recent Undo.
func (e *Engine) ReplayLog() []string {
	out := make([]string, len(e.replayLog))
	copy(out, e.replayLog)
	return out
}

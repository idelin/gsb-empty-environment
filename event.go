package detrun

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Kind identifies the type of a recorded event.
type Kind int

const (
	// KindSchedule records the scheduler's choice of which task runs next.
	KindSchedule Kind = iota
	// KindClock records a virtual clock advance. The clock never follows
	// wall time, so two runs cannot hide divergent trajectories behind
	// real-time differences.
	KindClock
	// KindExternal records an external request issued by a task together
	// with the result (or failure) the task observed.
	KindExternal
)

// Event is one entry in the recording. Seq is assigned by the Recorder.
type Event struct {
	Seq  int
	Kind Kind
	Task int           // task id, -1 for clock events
	Now  time.Duration // virtual time at which the event happened
	Req  string        // external request payload (KindExternal)
	Resp string        // external response delivered to the task (KindExternal)
	Err  string        // external failure message delivered to the task, "" on success
}

func describe(e Event) string {
	switch e.Kind {
	case KindSchedule:
		return fmt.Sprintf("schedule(task=%d, now=%s)", e.Task, e.Now)
	case KindClock:
		return fmt.Sprintf("clock(advance to %s)", e.Now)
	case KindExternal:
		return fmt.Sprintf("external(task=%d, req=%q, now=%s)", e.Task, e.Req, e.Now)
	}
	return fmt.Sprintf("unknown(kind=%d)", e.Kind)
}

// matches reports whether a recorded event agrees with an event produced
// during replay. Response fields are outputs injected into the run, so they
// are intentionally not compared.
func matches(recorded, got Event) bool {
	if recorded.Kind != got.Kind || recorded.Task != got.Task || recorded.Now != got.Now {
		return false
	}
	if recorded.Kind == KindExternal && recorded.Req != got.Req {
		return false
	}
	return true
}

// DivergenceError reports the first position where a replay stopped matching
// the recording.
type DivergenceError struct {
	Pos      int
	Expected string
	Got      string
}

func (e *DivergenceError) Error() string {
	return fmt.Sprintf("replay diverged at event %d: expected %s, got %s", e.Pos, e.Expected, e.Got)
}

// IsDivergence reports whether err is a replay divergence.
func IsDivergence(err error) bool {
	var d *DivergenceError
	return errors.As(err, &d)
}

// Recorder appends events produced by a record-mode run.
type Recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *Recorder) append(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.Seq = len(r.events)
	r.events = append(r.events, e)
}

// Events returns a copy of the recorded log.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// Replayer consumes a recording during replay, verifying each produced event
// against the log in order.
type Replayer struct {
	mu  sync.Mutex
	log []Event
	pos int
}

func NewReplayer(log []Event) *Replayer {
	cp := make([]Event, len(log))
	copy(cp, log)
	return &Replayer{log: cp}
}

// expect verifies that the next produced event matches the log and returns
// the recorded event (carrying the recorded outputs to inject).
func (r *Replayer) expect(got Event) (Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pos >= len(r.log) {
		return Event{}, &DivergenceError{
			Pos:      r.pos,
			Expected: "end of recording",
			Got:      describe(got),
		}
	}
	recorded := r.log[r.pos]
	if !matches(recorded, got) {
		return Event{}, &DivergenceError{
			Pos:      r.pos,
			Expected: describe(recorded),
			Got:      describe(got),
		}
	}
	r.pos++
	return recorded, nil
}

// finish must be called once the run is over. A recording with unconsumed
// events means the run stopped early; that is a divergence, not a success.
func (r *Replayer) finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pos != len(r.log) {
		return &DivergenceError{
			Pos:      r.pos,
			Expected: describe(r.log[r.pos]),
			Got:      fmt.Sprintf("end of run (%d unconsumed event(s) left in recording)", len(r.log)-r.pos),
		}
	}
	return nil
}

// EventString renders an event in the same human-readable form used in
// divergence reports.
func EventString(e Event) string { return describe(e) }

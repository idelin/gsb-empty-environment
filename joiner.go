// Package joiner implements a two-stream temporal join with negation events.
//
// A left event L waits up to a configurable timeout for a matching right
// event R (matched by key):
//
//   - R arrives within the window  -> emit Matched immediately.
//   - window expires with no R     -> emit LeftOnly (orphan).
//   - a Cancel event for the key arrives before the timeout -> drop L
//     silently: no output is ever produced for it.
//   - R arrives after LeftOnly was already emitted (late data) -> emit a
//     LateMatched compensation result, provided it lands within the
//     configured lateness bound.
//
// Out-of-order traffic is tolerated: an early R is buffered until its own
// window expires (then emitted as RightOnly), and an early Cancel is
// remembered so a later L for the same key is suppressed.
package joiner

import (
	"sync"
	"time"
)

// Side identifies which stream an event belongs to.
type Side int

const (
	Left Side = iota
	Right
)

func (s Side) String() string {
	if s == Left {
		return "L"
	}
	return "R"
}

// Kind distinguishes data events from negation (cancel) events.
type Kind int

const (
	Data Kind = iota
	Cancel
)

// Event is a single record on either input stream.
type Event struct {
	Key     string
	Side    Side
	Kind    Kind
	Payload string
}

// ResultType classifies joiner outputs.
type ResultType int

const (
	// Matched: L and R paired within the window.
	Matched ResultType = iota
	// LeftOnly: L timed out with no matching R.
	LeftOnly
	// RightOnly: an out-of-order R never saw its L within the window.
	RightOnly
	// LateMatched: R arrived after LeftOnly was emitted; compensates the
	// earlier orphan output.
	LateMatched
)

func (t ResultType) String() string {
	switch t {
	case Matched:
		return "MATCHED"
	case LeftOnly:
		return "LEFT-ONLY"
	case RightOnly:
		return "RIGHT-ONLY"
	case LateMatched:
		return "LATE-MATCHED"
	}
	return "UNKNOWN"
}

// Result is one output record of the joiner.
type Result struct {
	Type  ResultType
	Key   string
	Left  *Event
	Right *Event
	At    time.Time
}

type leftState struct {
	event Event
	timer Timer
}

type rightState struct {
	event Event
	timer Timer
}

// Joiner is the streaming join component. It is safe for concurrent use.
// The emit callback is invoked synchronously, outside the internal lock.
type Joiner struct {
	mu       sync.Mutex
	clock    Clock
	timeout  time.Duration
	lateness time.Duration
	emit     func(Result)

	pendLeft  map[string]*leftState  // L waiting for R
	pendRight map[string]*rightState // early R waiting for L
	orphans   map[string]Timer       // LeftOnly emitted, late R may compensate
	cancelled map[string]Timer       // Cancel seen, late L suppressed
}

// New creates a Joiner. timeout is the match window (e.g. 5s); lateness is
// how long orphan/cancel tombstones are retained for late compensation.
func New(clock Clock, timeout, lateness time.Duration, emit func(Result)) *Joiner {
	return &Joiner{
		clock:     clock,
		timeout:   timeout,
		lateness:  lateness,
		emit:      emit,
		pendLeft:  make(map[string]*leftState),
		pendRight: make(map[string]*rightState),
		orphans:   make(map[string]Timer),
		cancelled: make(map[string]Timer),
	}
}

// Ingest feeds one event into the joiner.
func (j *Joiner) Ingest(e Event) {
	j.mu.Lock()
	var out []Result
	if e.Kind == Cancel {
		out = j.onCancel(e)
	} else if e.Side == Left {
		out = j.onLeft(e)
	} else {
		out = j.onRight(e)
	}
	j.mu.Unlock()
	for _, r := range out {
		j.emit(r)
	}
}

// StateSize reports the total number of retained state entries across all
// internal tables. It is 0 once every timer has fired or been cancelled,
// which the out-of-order tests use to prove zero state leakage.
func (j *Joiner) StateSize() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.pendLeft) + len(j.pendRight) + len(j.orphans) + len(j.cancelled)
}

func (j *Joiner) onLeft(e Event) []Result {
	key := e.Key
	// A cancel arrived before its L (out of order): consume it, drop L.
	if t, ok := j.cancelled[key]; ok {
		t.Stop()
		delete(j.cancelled, key)
		return nil
	}
	// An early R is already buffered: pair immediately.
	if st, ok := j.pendRight[key]; ok {
		st.timer.Stop()
		delete(j.pendRight, key)
		r := st.event
		return []Result{{
			Type: Matched, Key: key,
			Left: ptr(e), Right: ptr(r), At: j.clock.Now(),
		}}
	}
	st := &leftState{event: e}
	st.timer = j.clock.AfterFunc(j.timeout, func() { j.expireLeft(key) })
	j.pendLeft[key] = st
	return nil
}

func (j *Joiner) onRight(e Event) []Result {
	key := e.Key
	// L is waiting: pair immediately.
	if st, ok := j.pendLeft[key]; ok {
		st.timer.Stop()
		delete(j.pendLeft, key)
		l := st.event
		return []Result{{
			Type: Matched, Key: key,
			Left: ptr(l), Right: ptr(e), At: j.clock.Now(),
		}}
	}
	// L already timed out and LeftOnly was emitted: compensate.
	if t, ok := j.orphans[key]; ok {
		t.Stop()
		delete(j.orphans, key)
		return []Result{{
			Type: LateMatched, Key: key,
			Right: ptr(e), At: j.clock.Now(),
		}}
	}
	// The key was cancelled: drop the late R.
	if _, ok := j.cancelled[key]; ok {
		return nil
	}
	// No L yet (out of order): buffer R for its own window.
	st := &rightState{event: e}
	st.timer = j.clock.AfterFunc(j.timeout, func() { j.expireRight(key) })
	j.pendRight[key] = st
	return nil
}

func (j *Joiner) onCancel(e Event) []Result {
	key := e.Key
	// L is waiting: abandon the wait, emit nothing.
	if st, ok := j.pendLeft[key]; ok {
		st.timer.Stop()
		delete(j.pendLeft, key)
		return nil
	}
	// LeftOnly already went out: cancel the pending compensation window.
	if t, ok := j.orphans[key]; ok {
		t.Stop()
		delete(j.orphans, key)
		return nil
	}
	// Cancel arrived before its L (out of order): remember it briefly so
	// the late L is suppressed when it shows up.
	if _, ok := j.cancelled[key]; !ok {
		j.cancelled[key] = j.clock.AfterFunc(j.lateness, func() {
			j.mu.Lock()
			delete(j.cancelled, key)
			j.mu.Unlock()
		})
	}
	return nil
}

func (j *Joiner) expireLeft(key string) {
	j.mu.Lock()
	st, ok := j.pendLeft[key]
	if !ok {
		j.mu.Unlock()
		return
	}
	delete(j.pendLeft, key)
	j.orphans[key] = j.clock.AfterFunc(j.lateness, func() {
		j.mu.Lock()
		delete(j.orphans, key)
		j.mu.Unlock()
	})
	res := Result{
		Type: LeftOnly, Key: key,
		Left: ptr(st.event), At: j.clock.Now(),
	}
	j.mu.Unlock()
	j.emit(res)
}

func (j *Joiner) expireRight(key string) {
	j.mu.Lock()
	st, ok := j.pendRight[key]
	if !ok {
		j.mu.Unlock()
		return
	}
	delete(j.pendRight, key)
	res := Result{
		Type: RightOnly, Key: key,
		Right: ptr(st.event), At: j.clock.Now(),
	}
	j.mu.Unlock()
	j.emit(res)
}

func ptr(e Event) *Event { return &e }

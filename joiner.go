// Package streamjoin implements a two-stream temporal join with negation
// (cancel) events. A left event waits up to a configurable window for a
// matching right event; on timeout it emits a left-only result; a cancel
// event abandons the wait silently; late right events still complete the
// join (compensation) within a lateness tolerance.
package streamjoin

import (
	"sync"
	"time"
)

// EventKind identifies the stream an event belongs to.
type EventKind int

const (
	LeftEvent EventKind = iota
	RightEvent
	CancelEvent // negation event: cancels the join wait for its key
)

func (k EventKind) String() string {
	switch k {
	case LeftEvent:
		return "LEFT"
	case RightEvent:
		return "RIGHT"
	case CancelEvent:
		return "CANCEL"
	}
	return "UNKNOWN"
}

// Event is one input record. At is the event time used for window matching.
type Event struct {
	Kind EventKind
	ID   string
	At   time.Time
}

// ResultKind identifies the kind of join output.
type ResultKind int

const (
	// Paired: a left and a right matched within the window.
	Paired ResultKind = iota
	// LeftOnly: the left event timed out with no match and no cancel.
	LeftOnly
)

func (k ResultKind) String() string {
	if k == Paired {
		return "PAIRED"
	}
	return "LEFT-ONLY"
}

// Result is one output record of the join.
type Result struct {
	Kind  ResultKind
	ID    string
	Left  Event
	Right Event // set only for Paired
	Late  bool  // true when this pair compensates an earlier LeftOnly
}

// Stats exposes internal state sizes so callers (and tests) can verify
// there is no state leakage.
type Stats struct {
	PendingLefts   int
	BufferedRights int
	EmittedLefts   int
}

type leftState struct {
	ev     Event
	cancel func() // cancels the timeout timer
}

type bufferedRight struct {
	ev     Event
	cancel func() // cancels the buffer-expiry timer
}

// Joiner is a single-keyed-stream temporal join operator. It is safe for
// concurrent use; the emit callback is invoked without internal locks held.
type Joiner struct {
	window   time.Duration
	lateness time.Duration
	clock    Clock
	emit     func(Result)

	mu      sync.Mutex
	pending map[string]*leftState      // lefts waiting for a right
	rights  map[string][]bufferedRight // out-of-order rights waiting for a left
	emitted map[string]Event           // lefts already output as LeftOnly, kept for late compensation
}

// NewJoiner creates a Joiner. window is how long a left waits for its right.
// lateness is how long after a LeftOnly emission a late right may still
// produce a compensating Paired(Late=true) result. emit must not call back
// into the Joiner.
func NewJoiner(window, lateness time.Duration, clock Clock, emit func(Result)) *Joiner {
	return &Joiner{
		window:   window,
		lateness: lateness,
		clock:    clock,
		emit:     emit,
		pending:  make(map[string]*leftState),
		rights:   make(map[string][]bufferedRight),
		emitted:  make(map[string]Event),
	}
}

// withinWindow reports whether right event time r falls in (l, l+window].
func (j *Joiner) withinWindow(l, r time.Time) bool {
	return !r.Before(l) && !r.After(l.Add(j.window))
}

// Handle processes one input event.
func (j *Joiner) Handle(ev Event) {
	switch ev.Kind {
	case LeftEvent:
		j.handleLeft(ev)
	case RightEvent:
		j.handleRight(ev)
	case CancelEvent:
		j.handleCancel(ev.ID)
	}
}

func (j *Joiner) handleLeft(ev Event) {
	j.mu.Lock()
	// Out-of-order case: a matching right arrived earlier and is buffered.
	if buf, ok := j.rights[ev.ID]; ok {
		for i, br := range buf {
			if j.withinWindow(ev.At, br.ev.At) {
				br.cancel()
				j.rights[ev.ID] = append(buf[:i], buf[i+1:]...)
				if len(j.rights[ev.ID]) == 0 {
					delete(j.rights, ev.ID)
				}
				res := Result{Kind: Paired, ID: ev.ID, Left: ev, Right: br.ev}
				j.mu.Unlock()
				j.emit(res)
				return
			}
		}
	}
	if _, dup := j.pending[ev.ID]; dup {
		j.mu.Unlock()
		return
	}
	st := &leftState{ev: ev}
	deadline := ev.At.Add(j.window)
	d := deadline.Sub(j.clock.Now())
	if d < 0 {
		d = 0
	}
	st.cancel = j.clock.AfterFunc(d, func() { j.expireLeft(ev.ID) })
	j.pending[ev.ID] = st
	j.mu.Unlock()
}

func (j *Joiner) handleRight(ev Event) {
	j.mu.Lock()
	// Normal case: left is still waiting.
	if st, ok := j.pending[ev.ID]; ok && j.withinWindow(st.ev.At, ev.At) {
		st.cancel()
		delete(j.pending, ev.ID)
		res := Result{Kind: Paired, ID: ev.ID, Left: st.ev, Right: ev}
		j.mu.Unlock()
		j.emit(res)
		return
	}
	// Late-compensation case: left already timed out and emitted LeftOnly,
	// but this right's event time is inside the original window.
	if l, ok := j.emitted[ev.ID]; ok && j.withinWindow(l.At, ev.At) {
		delete(j.emitted, ev.ID)
		res := Result{Kind: Paired, ID: ev.ID, Left: l, Right: ev, Late: true}
		j.mu.Unlock()
		j.emit(res)
		return
	}
	// Out-of-order case: no left seen yet; buffer the right so a later
	// left can still match. The buffer entry expires after window+lateness
	// to guarantee no state leakage.
	br := bufferedRight{ev: ev}
	deadline := ev.At.Add(j.window + j.lateness)
	d := deadline.Sub(j.clock.Now())
	if d < 0 {
		d = 0
	}
	br.cancel = j.clock.AfterFunc(d, func() { j.purgeRight(ev.ID, ev.At) })
	j.rights[ev.ID] = append(j.rights[ev.ID], br)
	j.mu.Unlock()
}

func (j *Joiner) handleCancel(id string) {
	j.mu.Lock()
	if st, ok := j.pending[id]; ok {
		st.cancel()
		delete(j.pending, id)
	}
	for _, br := range j.rights[id] {
		br.cancel()
	}
	delete(j.rights, id)
	delete(j.emitted, id)
	j.mu.Unlock()
}

// expireLeft fires when a pending left's window elapses unmatched.
func (j *Joiner) expireLeft(id string) {
	j.mu.Lock()
	st, ok := j.pending[id]
	if !ok {
		j.mu.Unlock()
		return
	}
	delete(j.pending, id)
	if j.lateness > 0 {
		j.emitted[id] = st.ev
		deadline := st.ev.At.Add(j.window + j.lateness)
		d := deadline.Sub(j.clock.Now())
		if d < 0 {
			d = 0
		}
		j.clock.AfterFunc(d, func() { j.purgeEmitted(id) })
	}
	res := Result{Kind: LeftOnly, ID: id, Left: st.ev}
	j.mu.Unlock()
	j.emit(res)
}

// purgeRight drops one buffered right whose retention has expired.
func (j *Joiner) purgeRight(id string, at time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	buf := j.rights[id]
	for i, br := range buf {
		if br.ev.At.Equal(at) {
			j.rights[id] = append(buf[:i], buf[i+1:]...)
			break
		}
	}
	if len(j.rights[id]) == 0 {
		delete(j.rights, id)
	}
}

// purgeEmitted drops a LeftOnly record whose lateness tolerance has expired.
func (j *Joiner) purgeEmitted(id string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.emitted, id)
}

// Stats returns current internal state sizes.
func (j *Joiner) Stats() Stats {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, buf := range j.rights {
		n += len(buf)
	}
	return Stats{
		PendingLefts:   len(j.pending),
		BufferedRights: n,
		EmittedLefts:   len(j.emitted),
	}
}

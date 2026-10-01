// Package cep implements a multi-mode composite event sequence matcher
// built on a non-deterministic directed acyclic graph (NFA-DAG) of states.
//
// Supported pattern: A -> B with no C in between, within a sliding time
// window. The engine handles out-of-order events: a late-arriving C that
// falls inside an already-emitted match interval triggers a compensation
// (retraction) of that match, which is also recorded in the engine log.
// Partial matches that exceed the time window are discarded (timeout).
package cep

import (
	"fmt"
	"sort"
)

// Event is a single input event. Timestamp is logical event time in
// milliseconds; events may arrive out of order with respect to it.
type Event struct {
	Type      string
	Timestamp int64
}

// Match represents a completed A -> B sequence.
type Match struct {
	ID int
	A  Event
	B  Event
}

// NotificationKind distinguishes new hits from compensating retractions.
type NotificationKind int

const (
	NotifyMatch NotificationKind = iota
	NotifyRetract
)

func (k NotificationKind) String() string {
	if k == NotifyRetract {
		return "RETRACT"
	}
	return "MATCH"
}

// Notification is emitted to downstream consumers of the engine.
type Notification struct {
	Kind   NotificationKind
	Match  Match
	Reason string
}

// stateKind enumerates the NFA node kinds used to build the pattern graph.
type stateKind int

const (
	stateStart stateKind = iota
	stateA
	stateNegC // negation guard: any C inside the interval kills the run
	stateB
	stateFinal
)

// nfaState is a node of the NFA-DAG the pattern is compiled into.
type nfaState struct {
	name  string
	kind  stateKind
	edges []string // names of successor states (DAG: edges only go forward)
}

// compilePattern builds the NFA-DAG for  A -> B / no-C / window.
// Graph: start -> A -> (negC guard) -> B -> final.
func compilePattern() map[string]nfaState {
	return map[string]nfaState{
		"start": {name: "start", kind: stateStart, edges: []string{"A"}},
		"A":     {name: "A", kind: stateA, edges: []string{"negC"}},
		"negC":  {name: "negC", kind: stateNegC, edges: []string{"B"}},
		"B":     {name: "B", kind: stateB, edges: []string{"final"}},
		"final": {name: "final", kind: stateFinal},
	}
}

// Engine is the CEP runtime. It keeps open NFA runs (pending A's), a
// history of negation events, and the set of live (non-retracted) matches.
type Engine struct {
	window    int64 // sliding window in milliseconds
	graph     map[string]nfaState
	pendingA  []Event // open runs parked in state "A"
	cEvents   []Event // observed negation events (event-time ordered)
	matches   []Match // live matches
	nextID    int
	watermark int64 // max event timestamp seen so far
	logs      []string
}

// NewEngine creates an engine with the given sliding window (milliseconds).
func NewEngine(windowMillis int64) *Engine {
	return &Engine{
		window: windowMillis,
		graph:  compilePattern(),
		nextID: 1,
	}
}

// Logs returns the engine's internal audit log.
func (e *Engine) Logs() []string { return append([]string(nil), e.logs...) }

// ActiveMatches returns the currently live (non-retracted) matches.
func (e *Engine) ActiveMatches() []Match { return append([]Match(nil), e.matches...) }

// PendingCount reports how many A-runs are still open.
func (e *Engine) PendingCount() int { return len(e.pendingA) }

func (e *Engine) logf(format string, args ...any) {
	e.logs = append(e.logs, fmt.Sprintf(format, args...))
}

// Process feeds one event into the engine and returns the notifications
// produced by it (zero or more matches / retractions).
func (e *Engine) Process(ev Event) []Notification {
	if ev.Timestamp > e.watermark {
		e.watermark = ev.Timestamp
	}
	e.expireTimeout()

	var out []Notification
	switch ev.Type {
	case "A":
		e.pendingA = append(e.pendingA, ev)
		e.logf("t=%d state A: open run #%d (A@%d)", ev.Timestamp, len(e.pendingA), ev.Timestamp)
	case "C":
		e.cEvents = append(e.cEvents, ev)
		out = append(out, e.applyNegation(ev)...)
	case "B":
		out = append(out, e.completeRuns(ev)...)
	default:
		e.logf("t=%d unknown event type %q ignored", ev.Timestamp, ev.Type)
	}
	return out
}

// expireTimeout discards open runs whose window has elapsed relative to the
// current watermark (pattern timeout discard).
func (e *Engine) expireTimeout() {
	kept := e.pendingA[:0]
	for _, a := range e.pendingA {
		if e.watermark-a.Timestamp > e.window {
			e.logf("t=%d timeout: discard open run A@%d (window %dms exceeded)", e.watermark, a.Timestamp, e.window)
		} else {
			kept = append(kept, a)
		}
	}
	e.pendingA = kept
}

// applyNegation handles a C event: it kills open runs started before C and
// retracts already-emitted matches whose (A, B) interval contains C
// (out-of-order compensation).
func (e *Engine) applyNegation(c Event) []Notification {
	var out []Notification

	kept := e.pendingA[:0]
	for _, a := range e.pendingA {
		if a.Timestamp < c.Timestamp {
			e.logf("t=%d negation: discard open run A@%d (C@%d inside window)", c.Timestamp, a.Timestamp, c.Timestamp)
		} else {
			kept = append(kept, a)
		}
	}
	e.pendingA = kept

	live := e.matches[:0]
	for _, m := range e.matches {
		if m.A.Timestamp < c.Timestamp && c.Timestamp < m.B.Timestamp {
			reason := fmt.Sprintf("late C@%d inside match interval (%d, %d)", c.Timestamp, m.A.Timestamp, m.B.Timestamp)
			e.logf("t=%d compensate: retract match #%d A@%d -> B@%d: %s", c.Timestamp, m.ID, m.A.Timestamp, m.B.Timestamp, reason)
			out = append(out, Notification{Kind: NotifyRetract, Match: m, Reason: reason})
		} else {
			live = append(live, m)
		}
	}
	e.matches = live
	return out
}

// completeRuns fires matches for every open A-run that pairs with B inside
// the window and without an intervening C.
func (e *Engine) completeRuns(b Event) []Notification {
	var out []Notification
	kept := e.pendingA[:0]
	for _, a := range e.pendingA {
		switch {
		case a.Timestamp >= b.Timestamp:
			kept = append(kept, a) // B precedes A in event time; run stays open
		case b.Timestamp-a.Timestamp > e.window:
			kept = append(kept, a) // outside window; leave for timeout discard
		case e.hasNegationBetween(a.Timestamp, b.Timestamp):
			e.logf("t=%d negation: A@%d -> B@%d rejected (C inside interval)", b.Timestamp, a.Timestamp, b.Timestamp)
		default:
			m := Match{ID: e.nextID, A: a, B: b}
			e.nextID++
			e.matches = append(e.matches, m)
			e.logf("t=%d match #%d: A@%d -> B@%d (window %dms)", b.Timestamp, m.ID, a.Timestamp, b.Timestamp, e.window)
			out = append(out, Notification{Kind: NotifyMatch, Match: m})
		}
	}
	e.pendingA = kept
	return out
}

// hasNegationBetween reports whether any C event is known inside (from, to).
func (e *Engine) hasNegationBetween(from, to int64) bool {
	for _, c := range e.cEvents {
		if c.Timestamp > from && c.Timestamp < to {
			return true
		}
	}
	return false
}

// Flush advances the watermark to the given event time, expiring any open
// runs whose window has elapsed. Use it at end-of-input to force timeouts.
func (e *Engine) Flush(watermark int64) {
	if watermark > e.watermark {
		e.watermark = watermark
	}
	e.expireTimeout()
}

// GraphDescription renders the compiled NFA-DAG for inspection.
func (e *Engine) GraphDescription() string {
	names := make([]string, 0, len(e.graph))
	for name := range e.graph {
		names = append(names, name)
	}
	sort.Strings(names)
	s := ""
	for _, n := range names {
		st := e.graph[n]
		s += fmt.Sprintf("%s -> %v\n", st.name, st.edges)
	}
	return s
}

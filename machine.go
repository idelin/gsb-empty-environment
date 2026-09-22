// Package fsm compiles an explicit state-transition table and executes
// transitions atomically: an action's output and the state change are
// committed together; a failed action leaves the old state intact.
package fsm

import (
	"errors"
	"fmt"
)

// State names a node of the machine.
type State string

// Event names an input symbol of the machine.
type Event string

// Context carries the data a transition action may need.
type Context struct {
	Event Event
	Frame *Frame // nil for synthetic events such as timeouts
}

// Action produces the output of a transition. A non-nil error aborts the
// transition: the state change is NOT committed.
type Action func(ctx *Context) (interface{}, error)

// Row is one line of the explicit transition table: From --On--> To.
type Row struct {
	From   State
	On     Event
	To     State
	Action Action
}

// NoTransitionError reports that the table has no row for (From, On).
type NoTransitionError struct {
	From State
	On   Event
}

func (e *NoTransitionError) Error() string {
	return fmt.Sprintf("fsm: no transition for (%q, %q)", e.From, e.On)
}

// IsNoTransition reports whether err is a *NoTransitionError.
func IsNoTransition(err error) bool {
	_, ok := err.(*NoTransitionError)
	return ok
}

type transitionKey struct {
	from State
	on   Event
}

type transition struct {
	to     State
	action Action
}

// Machine is an immutable, validated transition table.
type Machine struct {
	initial State
	states  map[State]struct{}
	table   map[transitionKey]transition
}

// Compile validates the explicit table and builds an immutable machine.
// It rejects: empty/undeclared initial state, duplicate state declarations,
// rows referencing undeclared states, and duplicate (From, On) rows.
func Compile(initial State, states []State, rows []Row) (*Machine, error) {
	if initial == "" {
		return nil, errors.New("fsm: initial state is empty")
	}
	declared := make(map[State]struct{}, len(states))
	for _, s := range states {
		if s == "" {
			return nil, errors.New("fsm: empty state declared")
		}
		if _, dup := declared[s]; dup {
			return nil, fmt.Errorf("fsm: state %q declared twice", s)
		}
		declared[s] = struct{}{}
	}
	if _, ok := declared[initial]; !ok {
		return nil, fmt.Errorf("fsm: initial state %q not declared", initial)
	}
	table := make(map[transitionKey]transition, len(rows))
	for i, r := range rows {
		if _, ok := declared[r.From]; !ok {
			return nil, fmt.Errorf("fsm: row %d: unknown from-state %q", i, r.From)
		}
		if _, ok := declared[r.To]; !ok {
			return nil, fmt.Errorf("fsm: row %d: unknown to-state %q", i, r.To)
		}
		k := transitionKey{from: r.From, on: r.On}
		if _, dup := table[k]; dup {
			return nil, fmt.Errorf("fsm: duplicate transition (%q, %q)", r.From, r.On)
		}
		table[k] = transition{to: r.To, action: r.Action}
	}
	return &Machine{initial: initial, states: declared, table: table}, nil
}

// Initial returns the machine's initial state.
func (m *Machine) Initial() State { return m.initial }

// Result is the outcome of one Step.
type Result struct {
	Output       interface{} // action output (nil when the action failed)
	Applied      bool        // true when the state change was committed
	Deduplicated bool        // true when a replay was answered from cache
	Err          error       // action failure or missing transition
}

// Step runs the transition for (from, on). The action executes first; only
// on success are its output and the next state returned together, so a
// failed action keeps the old state.
func (m *Machine) Step(from State, on Event, ctx *Context) (State, Result) {
	t, ok := m.table[transitionKey{from: from, on: on}]
	if !ok {
		return from, Result{Err: &NoTransitionError{From: from, On: on}}
	}
	if ctx == nil {
		ctx = &Context{Event: on}
	}
	var out interface{}
	if t.action != nil {
		o, err := t.action(ctx)
		if err != nil {
			return from, Result{Err: err} // old state kept
		}
		out = o
	}
	return t.to, Result{Output: out, Applied: true}
}

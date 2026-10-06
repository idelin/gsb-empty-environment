// Package checker models two finite state machines communicating over
// capacity-one bidirectional message queues, and exhaustively explores the
// finite composite state space to find shortest deadlock traces.
//
// Fairness is NOT assumed: the explorer considers every enabled transition
// of either machine at every composite state, so a deadlock is reported
// whenever one is reachable under some (possibly unfair) schedule.
package checker

import "fmt"

// QueueCap is the capacity of each directional queue.
const QueueCap = 1

// Kind is the kind of a transition.
type Kind int

const (
	Internal Kind = iota // internal action, no queue interaction
	Send                 // send Msg on the machine's outgoing queue
	Recv                 // receive Msg from the machine's incoming queue
)

func (k Kind) String() string {
	switch k {
	case Send:
		return "send"
	case Recv:
		return "recv"
	default:
		return "internal"
	}
}

// Transition is a single edge of a state machine.
type Transition struct {
	Kind Kind
	Msg  string // message for Send/Recv, ignored for Internal
	To   int    // target local state
}

func (t Transition) String() string {
	if t.Kind == Internal {
		return fmt.Sprintf("internal -> %d", t.To)
	}
	return fmt.Sprintf("%s %q -> %d", t.Kind, t.Msg, t.To)
}

// Machine is a finite state machine: a map from local state to its outgoing
// transitions, plus a set of final (terminating) local states.
type Machine struct {
	Name   string
	Trans  map[int][]Transition
	Finals map[int]bool
}

// State is a composite system state.
type State struct {
	Loc [2]int      // local state of each machine
	Q   [2][]string // Q[i]: queue from machine i to machine 1-i
}

// Initial returns the initial composite state with both machines in local
// state 0 and empty queues.
func Initial() State {
	return State{Q: [2][]string{{}, {}}}
}

// key returns a canonical string key for visited-set membership.
func (s State) key() string {
	k := fmt.Sprintf("%d|%d|", s.Loc[0], s.Loc[1])
	for i := 0; i < 2; i++ {
		for _, m := range s.Q[i] {
			k += m + ","
		}
		k += ";"
	}
	return k
}

func (s State) String() string {
	return fmt.Sprintf("(loc=%v, q0->1=%v, q1->0=%v)", s.Loc, s.Q[0], s.Q[1])
}

// Step records one fired transition: which machine moved and how.
type Step struct {
	Machine int
	Trans   Transition
}

func (st Step) String() string {
	return fmt.Sprintf("M%d: %s", st.Machine, st.Trans)
}

// Enabled reports whether transition t of machine i is enabled in s.
func Enabled(s State, i int, t Transition) bool {
	switch t.Kind {
	case Internal:
		return true
	case Send:
		return len(s.Q[i]) < QueueCap
	case Recv:
		in := s.Q[1-i]
		return len(in) > 0 && in[0] == t.Msg
	}
	return false
}

// Apply fires transition t of machine i on s, returning the successor state.
// The caller must ensure Enabled(s, i, t) holds.
func Apply(s State, i int, t Transition) State {
	next := State{Loc: s.Loc}
	next.Q[0] = append([]string(nil), s.Q[0]...)
	next.Q[1] = append([]string(nil), s.Q[1]...)
	switch t.Kind {
	case Send:
		next.Q[i] = append(next.Q[i], t.Msg)
	case Recv:
		next.Q[1-i] = next.Q[1-i][1:]
	}
	next.Loc[i] = t.To
	return next
}

// Terminal reports whether s is a proper terminating state: both machines in
// a final local state and both queues empty.
func Terminal(s State, m [2]Machine) bool {
	return m[0].Finals[s.Loc[0]] && m[1].Finals[s.Loc[1]] &&
		len(s.Q[0]) == 0 && len(s.Q[1]) == 0
}

// EnabledSteps lists all enabled steps of both machines in s.
func EnabledSteps(s State, m [2]Machine) []Step {
	var out []Step
	for i := 0; i < 2; i++ {
		for _, t := range m[i].Trans[s.Loc[i]] {
			if Enabled(s, i, t) {
				out = append(out, Step{Machine: i, Trans: t})
			}
		}
	}
	return out
}

// Deadlock reports whether s is a deadlock: non-terminal with no enabled step.
func Deadlock(s State, m [2]Machine) bool {
	return !Terminal(s, m) && len(EnabledSteps(s, m)) == 0
}

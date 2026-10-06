// Package checker models two finite-state machines communicating over two
// unidirectional FIFO queues of capacity one (one queue per direction),
// exhaustively explores the finite product state space, and finds shortest
// deadlock traces. Fairness is NOT assumed: every interleaving is explored.
package checker

import "fmt"

// ActionKind classifies what a transition does.
type ActionKind int

const (
	// Tau is an internal action with no queue interaction.
	Tau ActionKind = iota
	// Send appends Message to the machine's outgoing queue.
	// Enabled only when the outgoing queue has free capacity.
	Send
	// Recv consumes the head of the machine's incoming queue.
	// Enabled only when the head message equals Message.
	Recv
)

func (k ActionKind) String() string {
	switch k {
	case Tau:
		return "tau"
	case Send:
		return "send"
	case Recv:
		return "recv"
	}
	return "?"
}

// Action is the label on a transition.
type Action struct {
	Kind    ActionKind
	Message string // payload for Send; expected head for Recv; "" for Tau
}

// Transition moves a machine From one state To another via Act.
type Transition struct {
	From string
	To   string
	Act  Action
}

func (t Transition) String() string {
	switch t.Act.Kind {
	case Tau:
		return fmt.Sprintf("%s --tau--> %s", t.From, t.To)
	case Send:
		return fmt.Sprintf("%s --send(%s)--> %s", t.From, t.Act.Message, t.To)
	default:
		return fmt.Sprintf("%s --recv(%s)--> %s", t.From, t.Act.Message, t.To)
	}
}

// Machine is a finite-state machine. A state with no outgoing transitions
// is a terminal (successfully terminated) state.
type Machine struct {
	Name        string
	Initial     string
	Transitions []Transition
}

// Outgoing returns all transitions leaving state s.
func (m *Machine) Outgoing(s string) []Transition {
	var out []Transition
	for _, t := range m.Transitions {
		if t.From == s {
			out = append(out, t)
		}
	}
	return out
}

// Terminal reports whether state s has no outgoing transitions.
func (m *Machine) Terminal(s string) bool {
	return len(m.Outgoing(s)) == 0
}

// QueueCap is the capacity of each directional queue.
const QueueCap = 1

// State is a global configuration: both machine states plus both queues.
// QAB flows machine A -> machine B; QBA flows machine B -> machine A.
type State struct {
	A   string
	B   string
	QAB []string
	QBA []string
}

// Key returns a canonical hashable form of the state.
func (s State) Key() string {
	k := s.A + "|" + s.B + "|"
	for _, m := range s.QAB {
		k += m + ","
	}
	k += "|"
	for _, m := range s.QBA {
		k += m + ","
	}
	return k
}

func (s State) String() string {
	return fmt.Sprintf("(A=%s, B=%s, A->B=%v, B->A=%v)", s.A, s.B, s.QAB, s.QBA)
}

// Terminal reports whether both machines have terminated.
func (s State) Terminal(a, b *Machine) bool {
	return a.Terminal(s.A) && b.Terminal(s.B)
}

// Step is one executed transition plus the resulting global state.
type Step struct {
	Who        int // 0 = machine A, 1 = machine B
	Transition Transition
	After      State
}

func (st Step) String() string {
	who := "A"
	if st.Who == 1 {
		who = "B"
	}
	return fmt.Sprintf("%s: %s  =>  %s", who, st.Transition, st.After)
}

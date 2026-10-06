// Package dfacmp decides whether two deterministic finite automata accept
// the same language. Missing transitions fall into an implicit rejecting
// sink, the empty word is always checked, and the shortest (then
// lexicographically smallest) distinguishing word is reported together with
// its product-state path.
//
// States may carry unexpanded external fragments. While any fragment is
// unexpanded the comparison can only answer "counterexample" or "unknown"
// (with the blocking fragment ids); equivalence is claimed only once every
// fragment has been expanded and the full product graph has been explored.
package dfacmp

import (
	"fmt"
	"sort"
)

// Symbol is a single input letter.
type Symbol = string

// FragmentID identifies an external fragment attached to a state.
type FragmentID = string

// StateID identifies a state within one automaton.
type StateID int

// Sink is the implicit rejecting sink: every missing transition leads here
// and it accepts nothing.
const Sink StateID = -1

// NewStateRef encodes a reference to the i-th state of
// FragmentData.NewStates for use inside FragmentData.Transitions.
func NewStateRef(i int) StateID { return StateID(-2 - i) }

// Fragment is an external piece of automaton structure attached to a state.
// Until Expanded is true, the owning state is "opaque": its acceptance and
// outgoing transitions are unknown.
type Fragment struct {
	ID          FragmentID
	Expanded    bool
	Accepting   bool
	Transitions map[Symbol]StateID
}

// State is one DFA state. Transitions may be partial; missing symbols lead
// to Sink. If Fragment is non-nil and unexpanded, the state's own Accepting
// and Transitions are ignored until the fragment is expanded.
type State struct {
	Accepting   bool
	Transitions map[Symbol]StateID
	Fragment    *Fragment
}

func (s *State) opaque() bool {
	return s != nil && s.Fragment != nil && !s.Fragment.Expanded
}

func (s *State) accepting() bool {
	if s == nil {
		return false // sink rejects
	}
	if s.Fragment != nil && s.Fragment.Expanded {
		return s.Fragment.Accepting
	}
	return s.Accepting
}

func (s *State) next(sym Symbol) StateID {
	if s == nil {
		return Sink
	}
	if s.Fragment != nil && s.Fragment.Expanded {
		if t, ok := s.Fragment.Transitions[sym]; ok {
			return t
		}
		return Sink
	}
	if t, ok := s.Transitions[sym]; ok {
		return t
	}
	return Sink
}

// Automaton is a deterministic finite automaton. Alphabet must list every
// symbol that may appear in input words; it does not need to be sorted.
type Automaton struct {
	Name     string
	Alphabet []Symbol
	Start    StateID
	States   []State

	version uint64
}

// Version is bumped on every mutation. Query results are bound to the
// versions they were computed from.
func (a *Automaton) Version() uint64 { return a.version }

func (a *Automaton) state(id StateID) *State {
	if id < 0 || int(id) >= len(a.States) {
		return nil
	}
	return &a.States[id]
}

// IsAccepting reports whether id is an accepting state. known is false when
// the state carries an unexpanded fragment. The sink rejects.
func (a *Automaton) IsAccepting(id StateID) (accepting, known bool) {
	st := a.state(id)
	if st == nil {
		return false, true
	}
	if st.opaque() {
		return false, false
	}
	return st.accepting(), true
}

// IsOpaque reports whether id is a state with an unexpanded fragment.
func (a *Automaton) IsOpaque(id StateID) bool { return a.state(id).opaque() }

// Step follows sym from id. Missing transitions yield Sink, and so does
// stepping out of an opaque state; use IsOpaque to tell the two apart.
func (a *Automaton) Step(id StateID, sym Symbol) StateID {
	st := a.state(id)
	if st == nil || st.opaque() {
		return Sink
	}
	return st.next(sym)
}

// Run simulates word. known is false if the walk enters an opaque state.
func (a *Automaton) Run(word []Symbol) (accepts, known bool) {
	cur := a.Start
	for _, sym := range word {
		if st := a.state(cur); st != nil && st.opaque() {
			return false, false
		}
		cur = a.Step(cur, sym)
	}
	return a.IsAccepting(cur)
}

// Accepts reports whether the automaton accepts word, treating opaque
// states as rejecting. Use Run when fragments may be unexpanded.
func (a *Automaton) Accepts(word []Symbol) bool {
	acc, _ := a.Run(word)
	return acc
}

// AddState appends a state and returns its id.
func (a *Automaton) AddState(s State) StateID {
	a.States = append(a.States, s)
	a.version++
	return StateID(len(a.States) - 1)
}

// FragmentData describes the expansion of a fragment.
type FragmentData struct {
	Accepting bool
	// NewStates are appended to the automaton, in order, before
	// Transitions is installed.
	NewStates []State
	// Transitions maps symbols to existing StateIDs, to Sink, or to the
	// i-th entry of NewStates via NewStateRef(i).
	Transitions map[Symbol]StateID
}

// ExpandFragment fills the fragment id, bumping the automaton version.
func (a *Automaton) ExpandFragment(id FragmentID, data FragmentData) error {
	for i := range a.States {
		f := a.States[i].Fragment
		if f == nil || f.ID != id || f.Expanded {
			continue
		}
		base := StateID(len(a.States))
		tr := make(map[Symbol]StateID, len(data.Transitions))
		for sym, t := range data.Transitions {
			switch {
			case t <= -2:
				idx := int(-2 - t)
				if idx >= len(data.NewStates) {
					return fmt.Errorf("dfacmp: fragment %q: NewStates[%d] out of range", id, idx)
				}
				tr[sym] = base + StateID(idx)
			case t == Sink:
				tr[sym] = Sink
			case t < 0 || t >= base:
				return fmt.Errorf("dfacmp: fragment %q: transition to unknown state %d", id, t)
			default:
				tr[sym] = t
			}
		}
		a.States = append(a.States, data.NewStates...)
		f.Expanded = true
		f.Accepting = data.Accepting
		f.Transitions = tr
		a.version++
		return nil
	}
	return fmt.Errorf("dfacmp: fragment %q not found or already expanded", id)
}

// UnexpandedFragments lists the ids of fragments that are not yet expanded,
// in sorted order.
func (a *Automaton) UnexpandedFragments() []FragmentID {
	seen := map[FragmentID]bool{}
	var out []FragmentID
	for i := range a.States {
		if f := a.States[i].Fragment; f != nil && !f.Expanded && !seen[f.ID] {
			seen[f.ID] = true
			out = append(out, f.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Clone returns a deep copy, including the version counter.
func (a *Automaton) Clone() *Automaton {
	b := &Automaton{
		Name:     a.Name,
		Alphabet: append([]Symbol(nil), a.Alphabet...),
		Start:    a.Start,
		States:   make([]State, len(a.States)),
		version:  a.version,
	}
	for i, s := range a.States {
		ns := State{Accepting: s.Accepting, Transitions: cloneTransitions(s.Transitions)}
		if s.Fragment != nil {
			f := *s.Fragment
			f.Transitions = cloneTransitions(f.Transitions)
			ns.Fragment = &f
		}
		b.States[i] = ns
	}
	return b
}

func cloneTransitions(tr map[Symbol]StateID) map[Symbol]StateID {
	if tr == nil {
		return nil
	}
	out := make(map[Symbol]StateID, len(tr))
	for k, v := range tr {
		out[k] = v
	}
	return out
}

package ormap

import "fmt"

// DotConflictError is returned when two states assign different values to
// the same dot. A dot identifies a single assignment event, so diverging
// content for it indicates corruption or a violated replica protocol and
// is rejected rather than silently resolved.
type DotConflictError struct {
	Key   string
	Dot   Dot
	Value string
	Other string
}

func (e *DotConflictError) Error() string {
	return fmt.Sprintf("ormap: dot %s/%d on key %q carries conflicting values %q and %q",
		e.Dot.Replica, e.Dot.Counter, e.Key, e.Value, e.Other)
}

// State is an observed-remove multi-value map: a dot store of live
// assignments plus a causal context of every dot ever observed.
//
// Op is an alias: operations are delta-states, so the exact same merge
// gives op-based dissemination, op merging, and state-based merging with
// commutative, associative and idempotent semantics.
type State struct {
	Store   map[string]map[Dot]string
	Context *CausalContext
}

// Op is a delta-state produced by Set/Remove (or built directly with
// NewSetOp/NewRemoveOp). Ops merge exactly like states.
type Op = State

func NewState() *State {
	return &State{
		Store:   make(map[string]map[Dot]string),
		Context: NewCausalContext(),
	}
}

// NewSetOp builds the delta for an assignment of value to key under the
// given dot. The dot enters the causal context immediately so the delta
// is self-sufficient and idempotent on redelivery.
func NewSetOp(replica string, counter uint64, key, value string) *Op {
	dot := Dot{Replica: replica, Counter: counter}
	op := NewState()
	op.put(key, dot, value)
	op.Context.Insert(dot)
	return op
}

// NewRemoveOp builds the delta for an observed-remove: it carries only the
// dots of key that were observed when the remove was issued. Concurrent
// assignments whose dots are not listed are unaffected.
func NewRemoveOp(key string, observed ...Dot) *Op {
	op := NewState()
	for _, dot := range observed {
		op.Context.Insert(dot)
	}
	return op
}

func (s *State) put(key string, dot Dot, value string) {
	dots, ok := s.Store[key]
	if !ok {
		dots = make(map[Dot]string)
		s.Store[key] = dots
	}
	dots[dot] = value
}

func hasDot(store map[string]map[Dot]string, key string, dot Dot) bool {
	dots, ok := store[key]
	if !ok {
		return false
	}
	_, ok = dots[dot]
	return ok
}

// Merge joins o into s. It is commutative, associative and idempotent.
// An entry survives in the join iff its dot is not covered by the other
// side's causal context, or the other side still stores it live.
func (s *State) Merge(o *State) error {
	for key, dots := range o.Store {
		for dot, value := range dots {
			if existing, ok := s.Store[key][dot]; ok && existing != value {
				return &DotConflictError{Key: key, Dot: dot, Value: existing, Other: value}
			}
		}
	}
	merged := NewState()
	for key, dots := range s.Store {
		for dot, value := range dots {
			if o.Context.Contains(dot) && !hasDot(o.Store, key, dot) {
				continue
			}
			merged.put(key, dot, value)
		}
	}
	for key, dots := range o.Store {
		for dot, value := range dots {
			if s.Context.Contains(dot) && !hasDot(s.Store, key, dot) {
				continue
			}
			merged.put(key, dot, value)
		}
	}
	merged.Context = s.Context.Clone()
	merged.Context.Merge(o.Context)
	s.Store = merged.Store
	s.Context = merged.Context
	return nil
}

func (s *State) Clone() *State {
	out := NewState()
	for key, dots := range s.Store {
		for dot, value := range dots {
			out.put(key, dot, value)
		}
	}
	out.Context = s.Context.Clone()
	return out
}

func (s *State) Equal(o *State) bool {
	if len(s.Store) != len(o.Store) {
		return false
	}
	for key, dots := range s.Store {
		otherDots, ok := o.Store[key]
		if !ok || len(dots) != len(otherDots) {
			return false
		}
		for dot, value := range dots {
			if otherValue, ok := otherDots[dot]; !ok || otherValue != value {
				return false
			}
		}
	}
	return s.Context.Equal(o.Context)
}

// Live returns every surviving assignment for key, keyed by its dot.
// All concurrent values are preserved; nothing is dropped for display.
func (s *State) Live(key string) map[Dot]string {
	out := make(map[Dot]string)
	for dot, value := range s.Store[key] {
		out[dot] = value
	}
	return out
}

// Display resolves a single presentation value for key: the live
// assignment with the highest logical time, ties broken by replica ID.
// conflict reports whether more than one assignment is live. Display is
// pure: it never mutates or prunes the live set.
func (s *State) Display(key string) (value string, conflict bool) {
	live := s.Store[key]
	if len(live) == 0 {
		return "", false
	}
	var best Dot
	first := true
	for dot := range live {
		if first || best.Less(dot) {
			best = dot
			first = false
		}
	}
	return live[best], len(live) > 1
}

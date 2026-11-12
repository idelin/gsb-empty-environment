package netsec

import (
	"fmt"
	"strings"
)

// OrderKind distinguishes weak ("happens no later than") from strict
// ("happens strictly before") ordering between two events.
type OrderKind int

const (
	// WeakOrder requires Before <= After (the two events may coincide).
	WeakOrder OrderKind = iota
	// StrictOrder requires Before < After, i.e. Before+1 <= After on the
	// integer time line.
	StrictOrder
)

// Order is one precedence constraint between two events.
type Order struct {
	Before string
	After  string
	Kind   OrderKind
}

// Problem is an offline time-mutation instance: named events, each with an
// inclusive integer time window, plus a partial order over the events.
//
// The zero value is not usable; build instances with [NewProblem].
type Problem struct {
	names  []string
	index  map[string]int
	lo     []int64
	hi     []int64
	orders []Order
}

// NewProblem builds a problem from parallel name/bound slices and ordering
// constraints. Bounds are inclusive and must satisfy lo[i] <= hi[i]; every
// order endpoint must reference a declared event.
func NewProblem(names []string, lo, hi []int64, orders []Order) (*Problem, error) {
	n := len(names)
	if n == 0 {
		return nil, fmt.Errorf("netsec: at least one event is required")
	}
	if len(lo) != n || len(hi) != n {
		return nil, fmt.Errorf("netsec: lo/hi length mismatch: got %d names, %d lo, %d hi", n, len(lo), len(hi))
	}
	index := make(map[string]int, n)
	for i, name := range names {
		if name == "" {
			return nil, fmt.Errorf("netsec: event %d has an empty name", i)
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("netsec: duplicate event name %q", name)
		}
		if lo[i] > hi[i] {
			return nil, fmt.Errorf("netsec: event %q has empty window [%d, %d]", name, lo[i], hi[i])
		}
		index[name] = i
	}
	for k, ord := range orders {
		if _, ok := index[ord.Before]; !ok {
			return nil, fmt.Errorf("netsec: order %d references unknown event %q", k, ord.Before)
		}
		if _, ok := index[ord.After]; !ok {
			return nil, fmt.Errorf("netsec: order %d references unknown event %q", k, ord.After)
		}
		if ord.Kind != WeakOrder && ord.Kind != StrictOrder {
			return nil, fmt.Errorf("netsec: order %d has invalid kind %d", k, ord.Kind)
		}
	}

	p := &Problem{
		names:  append([]string(nil), names...),
		index:  index,
		lo:     append([]int64(nil), lo...),
		hi:     append([]int64(nil), hi...),
		orders: append([]Order(nil), orders...),
	}
	return p, nil
}

// EventCount returns the number of declared events.
func (p *Problem) EventCount() int { return len(p.names) }

// Event returns the name and inclusive integer window of event i.
func (p *Problem) Event(i int) (name string, lo, hi int64) {
	return p.names[i], p.lo[i], p.hi[i]
}

// EventIndex resolves an event name to its stable index.
func (p *Problem) EventIndex(name string) (int, bool) {
	i, ok := p.index[name]
	return i, ok
}

// Orders returns a copy of the declared ordering constraints.
func (p *Problem) Orders() []Order {
	return append([]Order(nil), p.orders...)
}

// Deadline is the smallest event upper bound; it is the global deadline of
// the instance.
func (p *Problem) Deadline() int64 {
	d := p.hi[0]
	for _, h := range p.hi[1:] {
		if h < d {
			d = h
		}
	}
	return d
}

// Legal reports whether the assignment is defined for every event, respects
// every integer window, and satisfies every ordering constraint.
func (p *Problem) Legal(t []int64) bool {
	if len(t) != len(p.names) {
		return false
	}
	for i := range t {
		if t[i] < p.lo[i] || t[i] > p.hi[i] {
			return false
		}
	}
	for _, ord := range p.orders {
		b := t[p.index[ord.Before]]
		a := t[p.index[ord.After]]
		if ord.Kind == StrictOrder {
			if !(b < a) {
				return false
			}
		} else if !(b <= a) {
			return false
		}
	}
	return true
}

// describe renders an internal edge back into the human-readable constraint
// it encodes, for diagnostics and negative-cycle evidence.
func (p *Problem) describe(e edge) string {
	from, to := e.from, e.to
	switch {
	case from == anchorNode:
		return fmt.Sprintf("t[%s] <= %d", p.names[to-1], e.weight)
	case to == anchorNode:
		return fmt.Sprintf("%d <= t[%s]", -e.weight, p.names[from-1])
	default:
		op := "<="
		if e.weight == 1 {
			op = "<"
		}
		return fmt.Sprintf("t[%s] %s t[%s] + %d", p.names[to-1], op, p.names[from-1], e.weight)
	}
}

// String renders the problem in a compact, deterministic form.
func (p *Problem) String() string {
	var b strings.Builder
	b.WriteString("Problem{\n")
	for i, name := range p.names {
		fmt.Fprintf(&b, "  %s in [%d, %d]\n", name, p.lo[i], p.hi[i])
	}
	for _, ord := range p.orders {
		op := "<="
		if ord.Kind == StrictOrder {
			op = "<"
		}
		fmt.Fprintf(&b, "  %s %s %s\n", ord.Before, op, ord.After)
	}
	b.WriteString("}")
	return b.String()
}

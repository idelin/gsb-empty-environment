// Package dvv implements Dotted Version Vectors (DVV), a causality
// tracking mechanism for leaderless replication.
//
// Unlike classic version vectors, which attach a full vector to every
// stored value and therefore accumulate "ghost branches" (stale sibling
// entries that are already causally superseded), DVV identifies each
// write with a single dot (NodeID, Counter) plus a compact causal
// context. Superseded values are discarded precisely, duplicate
// deliveries are absorbed idempotently, and only genuinely concurrent
// writes remain as siblings.
package dvv

import (
	"bytes"
	"fmt"
	"sort"
)

// Dot uniquely identifies a single write event: the node that performed
// it and a monotonically increasing counter local to that node.
type Dot struct {
	Node    string
	Counter uint64
}

func (d Dot) String() string { return fmt.Sprintf("%s:%d", d.Node, d.Counter) }

// VV is a version vector used as a causal context: the set of dots a
// write has already observed, summarised as the maximum counter seen
// per node.
type VV map[string]uint64

func (v VV) clone() VV {
	c := make(VV, len(v))
	for n, c2 := range v {
		c[n] = c2
	}
	return c
}

// covers reports whether v causally covers o, i.e. v >= o pointwise.
func (v VV) covers(o VV) bool {
	for n, c := range o {
		if v[n] < c {
			return false
		}
	}
	return true
}

// merge returns the pointwise maximum (join) of v and o.
func (v VV) merge(o VV) VV {
	m := v.clone()
	for n, c := range o {
		if m[n] < c {
			m[n] = c
		}
	}
	return m
}

func (v VV) String() string {
	nodes := make([]string, 0, len(v))
	for n := range v {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	var b bytes.Buffer
	b.WriteByte('{')
	for i, n := range nodes {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s:%d", n, v[n])
	}
	b.WriteByte('}')
	return b.String()
}

// Entry is one value in the set: the dot of the write that produced it,
// the causal context in which that write happened (excluding the dot
// itself), and the stored value.
type Entry struct {
	Dot   Dot
	Ctx   VV
	Value []byte
}

// causal returns the full causal knowledge of the entry: its context
// plus its own dot.
func (e Entry) causal() VV {
	c := e.Ctx.clone()
	if c[e.Dot.Node] < e.Dot.Counter {
		c[e.Dot.Node] = e.Dot.Counter
	}
	return c
}

func (e Entry) sameAs(o Entry) bool {
	if e.Dot != o.Dot || !bytes.Equal(e.Value, o.Value) || len(e.Ctx) != len(o.Ctx) {
		return false
	}
	for n, c := range e.Ctx {
		if o.Ctx[n] != c {
			return false
		}
	}
	return true
}

// Set is a DVVSet: the minimal list of causally non-dominated entries
// for one key. A converged, conflict-free key holds exactly one entry.
type Set struct {
	Entries []Entry
}

// New returns an empty set.
func New() *Set { return &Set{} }

// Context returns the join of the causal contexts of all entries: the
// causal history a client must present (or extend) for its next write.
func (s *Set) Context() VV {
	ctx := VV{}
	for _, e := range s.Entries {
		ctx = ctx.merge(e.causal())
	}
	return ctx
}

// Values returns the currently visible values. More than one value
// means the key has genuinely concurrent (conflicting) writes.
func (s *Set) Values() [][]byte {
	out := make([][]byte, 0, len(s.Entries))
	for _, e := range s.Entries {
		out = append(out, e.Value)
	}
	return out
}

// GraphSize reports the size of the version graph: the number of live
// branches (entries) and the total number of (node, counter) pairs
// stored across all contexts.
func (s *Set) GraphSize() (branches, contextPairs int) {
	branches = len(s.Entries)
	for _, e := range s.Entries {
		contextPairs += len(e.Ctx) + 1 // +1 for the dot itself
	}
	return branches, contextPairs
}

// add inserts e into the set, discarding entries dominated by e and
// ignoring e if it is already dominated (duplicate / stale delivery).
func (s *Set) add(e Entry) *Set {
	ec := e.causal()
	out := make([]Entry, 0, len(s.Entries)+1)
	for _, o := range s.Entries {
		oc := o.causal()
		switch {
		case oc.covers(ec):
			// Already known: duplicate or stale broadcast, absorb.
			return s
		case ec.covers(oc):
			// e supersedes o: drop the ghost branch.
			continue
		default:
			out = append(out, o)
		}
	}
	return &Set{Entries: append(out, e)}
}

// Update performs a coordinated write: it observes the whole current
// set, so the new dot causally supersedes every existing entry and the
// result always collapses to a single branch. Rewriting the identical
// value (an "empty update") therefore leaves no redundant branch.
func (s *Set) Update(node string, value []byte) *Set {
	ctx := s.Context()
	cnt := ctx[node] + 1
	// The entry context excludes the dot itself; ctx already holds the
	// node's previous counter, which is exactly cnt-1.
	return &Set{Entries: []Entry{{
		Dot:   Dot{Node: node, Counter: cnt},
		Ctx:   ctx,
		Value: append([]byte(nil), value...),
	}}}
}

// Event performs a leaderless server-side write on behalf of a client
// that supplied causal context ctx (typically obtained from a prior
// read). The write gets the next dot for node beyond ctx. Entries
// dominated by the new event are discarded; entries concurrent with it
// are kept as siblings.
//
// Contract: ctx must include the writer's own latest dot (i.e. the
// node issuing the event knows its last counter), so that dots stay
// globally unique. This is the standard DVV requirement that each node
// sequences its own writes.
func (s *Set) Event(ctx VV, node string, value []byte) *Set {
	e := Entry{
		Dot:   Dot{Node: node, Counter: ctx[node] + 1},
		Ctx:   ctx.clone(),
		Value: append([]byte(nil), value...),
	}
	return s.add(e)
}

// Join merges several replicas of the same key into one minimal set.
// The result is idempotent and order-independent: Join(a, b) ==
// Join(b, a) and Join(s, s) == s.
func Join(sets ...*Set) *Set {
	out := New()
	for _, s := range sets {
		for _, e := range s.Entries {
			out = out.add(e)
		}
	}
	return out
}

// Sync merges other into s and returns the minimal joined set.
func (s *Set) Sync(other *Set) *Set { return Join(s, other) }

// Equal reports whether two sets hold the same entries, regardless of
// entry order.
func (s *Set) Equal(o *Set) bool {
	if len(s.Entries) != len(o.Entries) {
		return false
	}
	used := make([]bool, len(o.Entries))
outer:
	for _, e := range s.Entries {
		for i, oe := range o.Entries {
			if !used[i] && e.sameAs(oe) {
				used[i] = true
				continue outer
			}
		}
		return false
	}
	return true
}

func (s *Set) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "DVVSet(%d entries)", len(s.Entries))
	for _, e := range s.Entries {
		fmt.Fprintf(&b, "\n  dot=%s ctx=%s value=%q", e.Dot, e.Ctx, e.Value)
	}
	return b.String()
}

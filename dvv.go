// Package dvv implements Dotted Version Vectors (DVV), a causality
// tracking mechanism for leaderless replicated stores.
//
// A classic version vector summarizes each node's history as a
// contiguous range [1..n]. That loses information: the clock {a:3}
// claims events a:1, a:2 and a:3 even if the writer never saw a:2,
// which makes stale writes falsely dominate newer values (ghost
// branches) or forces replicas to keep phantom siblings.
//
// DVV instead identifies every write with a dot — a point event
// (NodeID, Counter) — and represents a clock as a version vector plus
// the set of dots that fall outside the contiguous prefix. Two writes
// are ordered only when one's event set contains the other's, so
// duplicates of the same value are recognized precisely and only
// genuinely concurrent different values survive as siblings.
package dvv

import (
	"fmt"
	"sort"
	"strings"
)

// Dot uniquely identifies a single write event: the Counter-th write
// coordinated by Node.
type Dot struct {
	Node    string
	Counter uint64
}

func (d Dot) String() string { return fmt.Sprintf("(%s,%d)", d.Node, d.Counter) }

// Clock is a DVV clock: the set of events a value causally knows,
// stored as a contiguous version vector plus the individual dots beyond
// it. Invariant: dots[n] is sorted ascending and every dot > vv[n].
type Clock struct {
	vv   map[string]uint64
	dots map[string][]uint64
}

// EmptyClock returns the clock of a write with no causal past.
func EmptyClock() Clock { return Clock{} }

// NewContext builds a pure context clock (no dots) from a vector.
func NewContext(vv map[string]uint64) Clock {
	c := Clock{vv: make(map[string]uint64, len(vv))}
	for n, ctr := range vv {
		if ctr > 0 {
			c.vv[n] = ctr
		}
	}
	return c
}

// Size is the metadata footprint: vector entries plus loose dots.
func (c Clock) Size() int {
	n := len(c.vv)
	for _, ds := range c.dots {
		n += len(ds)
	}
	return n
}

// maxCounter returns the highest event counter known for node.
func (c Clock) maxCounter(node string) uint64 {
	m := c.vv[node]
	if ds := c.dots[node]; len(ds) > 0 && ds[len(ds)-1] > m {
		m = ds[len(ds)-1]
	}
	return m
}

// nodes returns the sorted node ids mentioned by the clock.
func (c Clock) nodes() []string {
	seen := map[string]bool{}
	for n := range c.vv {
		seen[n] = true
	}
	for n := range c.dots {
		seen[n] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func containsDot(ds []uint64, d uint64) bool {
	for _, x := range ds {
		if x == d {
			return true
		}
	}
	return false
}

// coversNode reports whether c's event set at node n includes the whole
// prefix [1..ovv] and every dot in odots.
func (c Clock) coversNode(n string, ovv uint64, odots []uint64) bool {
	cv := c.vv[n]
	if ovv > cv {
		// The gap (cv..ovv] must be filled contiguously by c's dots.
		cdots := c.dots[n]
		if uint64(len(cdots)) < ovv-cv {
			return false
		}
		for i := cv + 1; i <= ovv; i++ {
			if cdots[i-cv-1] != i {
				return false
			}
		}
	}
	for _, d := range odots {
		if d > cv && !containsDot(c.dots[n], d) {
			return false
		}
	}
	return true
}

// Descends reports whether c causally descends from o: every event
// summarized by o is also summarized by c.
func (c Clock) Descends(o Clock) bool {
	for _, n := range o.nodes() {
		if !c.coversNode(n, o.vv[n], o.dots[n]) {
			return false
		}
	}
	return true
}

// Equal reports whether both clocks summarize the same event set.
func (c Clock) Equal(o Clock) bool { return c.Descends(o) && o.Descends(c) }

// Concurrent reports whether neither clock descends from the other.
func (c Clock) Concurrent(o Clock) bool { return !c.Descends(o) && !o.Descends(c) }

// normalized folds every dot that extends the contiguous prefix into
// the vector, keeping the representation minimal. The event set is
// unchanged.
func (c Clock) normalized() Clock {
	out := Clock{vv: map[string]uint64{}, dots: map[string][]uint64{}}
	for _, n := range c.nodes() {
		v := c.vv[n]
		ds := c.dots[n]
		i := 0
		for i < len(ds) && ds[i] == v+1 {
			v++
			i++
		}
		if v > 0 {
			out.vv[n] = v
		}
		if i < len(ds) {
			rest := make([]uint64, len(ds)-i)
			copy(rest, ds[i:])
			out.dots[n] = rest
		}
	}
	return out
}

// withDot returns a normalized copy of c extended with the single event
// d. If d is already covered, the normalized clock is returned as is.
func (c Clock) withDot(d Dot) Clock {
	n := c.normalized()
	if d.Counter <= n.vv[d.Node] || containsDot(n.dots[d.Node], d.Counter) {
		return n
	}
	ds := append([]uint64{}, n.dots[d.Node]...)
	i := sort.Search(len(ds), func(i int) bool { return ds[i] >= d.Counter })
	ds = append(ds, 0)
	copy(ds[i+1:], ds[i:])
	ds[i] = d.Counter
	if n.dots == nil {
		n.dots = map[string][]uint64{}
	}
	n.dots[d.Node] = ds
	return n
}

// Merge returns the union of both event sets, normalized.
func (c Clock) Merge(o Clock) Clock {
	out := Clock{vv: map[string]uint64{}, dots: map[string][]uint64{}}
	for _, n := range c.nodes() {
		out.vv[n] = c.vv[n]
		if ds := c.dots[n]; len(ds) > 0 {
			out.dots[n] = append([]uint64{}, ds...)
		}
	}
	for _, n := range o.nodes() {
		if o.vv[n] > out.vv[n] {
			out.vv[n] = o.vv[n]
		}
		for _, d := range o.dots[n] {
			if !containsDot(out.dots[n], d) {
				out.dots[n] = append(out.dots[n], d)
			}
		}
		if ds := out.dots[n]; len(ds) > 0 {
			sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
			out.dots[n] = ds
		}
	}
	return out.normalized()
}

func (c Clock) String() string {
	var b strings.Builder
	b.WriteString("{")
	first := true
	for _, n := range c.nodes() {
		if !first {
			b.WriteString(" ")
		}
		first = false
		fmt.Fprintf(&b, "%s:%d", n, c.vv[n])
		for _, d := range c.dots[n] {
			fmt.Fprintf(&b, "+%d", d)
		}
	}
	b.WriteString("}")
	return b.String()
}

// Package mvr implements a multi-value register (MVR) replicated across
// many nodes. Version vectors distinguish causal overwrite from concurrent
// conflict: concurrent values are all retained, a successor write overwrites
// exactly the versions its origin has observed, and an explicit Resolve
// operation lets a client collapse a conflict set into a single value that
// then propagates like any other write.
//
// Wall-clock timestamps are carried on entries as metadata only. They are
// never consulted when deciding whether one version supersedes another, so
// a larger wall clock can never swallow a concurrent value.
package mvr

import (
	"fmt"
	"sort"
	"strings"
)

// VV is a version vector mapping replica ID to a logical counter.
type VV map[string]uint64

// Copy returns an independent copy of v.
func (v VV) Copy() VV {
	w := make(VV, len(v))
	for id, n := range v {
		w[id] = n
	}
	return w
}

// Join returns the component-wise maximum of a and b.
func Join(a, b VV) VV {
	out := a.Copy()
	for id, n := range b {
		if n > out[id] {
			out[id] = n
		}
	}
	return out
}

// Relation describes how two version vectors are ordered.
type Relation int

const (
	RelEqual      Relation = iota // identical versions
	RelBefore                     // strictly dominated (happened-before)
	RelAfter                      // strictly dominating
	RelConcurrent                 // incomparable: a conflict
)

func (r Relation) String() string {
	switch r {
	case RelEqual:
		return "equal"
	case RelBefore:
		return "before"
	case RelAfter:
		return "after"
	default:
		return "concurrent"
	}
}

// Compare orders a against b under the happens-before partial order.
func Compare(a, b VV) Relation {
	aLess, bLess := false, false
	for id, av := range a {
		bv := b[id]
		if av < bv {
			aLess = true
		}
		if av > bv {
			bLess = true
		}
	}
	for id, bv := range b {
		if _, ok := a[id]; !ok && bv > 0 {
			aLess = true
		}
	}
	switch {
	case aLess && bLess:
		return RelConcurrent
	case aLess:
		return RelBefore
	case bLess:
		return RelAfter
	default:
		return RelEqual
	}
}

func (v VV) String() string {
	ids := make([]string, 0, len(v))
	for id := range v {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s:%d", id, v[id]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// Entry is one (value, version) alternative stored in the register.
type Entry struct {
	Value  string
	Ver    VV
	Wall   int64  // wall-clock at write time; metadata only, never used for ordering
	Origin string // replica that produced this write
}

// Key uniquely identifies the write that produced the entry.
func (e Entry) Key() string { return e.Ver.String() }

func (e Entry) String() string {
	return fmt.Sprintf("%q ver=%s wall=%d origin=%s", e.Value, e.Ver, e.Wall, e.Origin)
}

// Register is the local state of one replica: a set of concurrent entries.
type Register struct {
	ID      string
	entries []Entry
}

// NewRegister returns an empty register owned by replica id.
func NewRegister(id string) *Register { return &Register{ID: id} }

// clock is this replica's knowledge: the join of all retained versions.
// Because evicted entries are always dominated by a retained one, the join
// of the retained set equals the join of everything ever applied.
func (r *Register) clock() VV {
	c := VV{}
	for _, e := range r.entries {
		c = Join(c, e.Ver)
	}
	return c
}

// Write records value locally. The new version dominates every version this
// replica has observed, so it causally overwrites exactly those versions and
// nothing else. It returns the entry to broadcast to peers.
func (r *Register) Write(value string, wall int64) Entry {
	ver := r.clock()
	ver[r.ID]++
	e := Entry{Value: value, Ver: ver, Wall: wall, Origin: r.ID}
	r.entries = []Entry{e}
	return e
}

// Resolve is the explicit conflict-resolution operation. Semantically it is
// a write issued after reading the current conflict set: its version
// dominates every version this replica has observed, so it supersedes the
// observed conflict set while leaving unobserved concurrent writes intact.
// The returned entry propagates to peers like any other write.
func (r *Register) Resolve(value string, wall int64) Entry {
	return r.Write(value, wall)
}

// Apply incorporates an entry received from a peer. It reports whether the
// local state changed. Delivery may be duplicated, delayed or reordered.
func (r *Register) Apply(e Entry) bool {
	for _, cur := range r.entries {
		switch Compare(e.Ver, cur.Ver) {
		case RelEqual, RelBefore:
			// Already known, or strictly dominated by a retained version.
			return false
		}
	}
	kept := make([]Entry, 0, len(r.entries)+1)
	for _, cur := range r.entries {
		if Compare(cur.Ver, e.Ver) != RelBefore {
			kept = append(kept, cur)
		}
	}
	r.entries = append(kept, e)
	return true
}

// Merge joins a peer's full entry set into this register (anti-entropy).
// The entry set forms a join-semilattice: keep every entry that is not
// strictly dominated by another entry in the union.
func (r *Register) Merge(remote []Entry) bool {
	before := r.keySet()
	all := make([]Entry, 0, len(r.entries)+len(remote))
	all = append(all, r.entries...)
	all = append(all, remote...)
	seen := map[string]bool{}
	kept := make([]Entry, 0, len(all))
	for i, e := range all {
		dominated := false
		for j, o := range all {
			if i != j && Compare(e.Ver, o.Ver) == RelBefore {
				dominated = true
				break
			}
		}
		if !dominated && !seen[e.Key()] {
			seen[e.Key()] = true
			kept = append(kept, e)
		}
	}
	r.entries = kept
	after := r.keySet()
	if len(before) != len(after) {
		return true
	}
	for k := range after {
		if !before[k] {
			return true
		}
	}
	return false
}

func (r *Register) keySet() map[string]bool {
	m := make(map[string]bool, len(r.entries))
	for _, e := range r.entries {
		m[e.Key()] = true
	}
	return m
}

// Entries returns the current conflict set, sorted for determinism.
func (r *Register) Entries() []Entry {
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Values returns the distinct currently visible values, sorted.
func (r *Register) Values() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range r.entries {
		if !seen[e.Value] {
			seen[e.Value] = true
			out = append(out, e.Value)
		}
	}
	sort.Strings(out)
	return out
}

// Equal reports whether both registers hold the same entry set.
func (r *Register) Equal(o *Register) bool {
	a, b := r.keySet(), o.keySet()
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

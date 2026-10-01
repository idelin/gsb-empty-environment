// Package awlwwmap implements an AW-LWW-Map: an Add-Wins Last-Writer-Wins
// Map CRDT with recursively nested sub-maps.
//
// Each key carries a set of add-timestamps and a set of remove-timestamps.
// A key is live iff its latest add is not strictly older than its latest
// remove (add-wins on ties). Writes to a nested field "touch" (re-add) every
// key on the path, so a concurrent remove of a parent key always loses to a
// causally-unaware field update, while a causally-later remove still wins.
package awlwwmap

import (
	"fmt"
	"sort"
	"strings"
)

// timestamp is a Lamport timestamp made unique and totally ordered by node ID.
type timestamp struct {
	time uint64
	node string
}

func (t timestamp) compare(o timestamp) int {
	switch {
	case t.time < o.time:
		return -1
	case t.time > o.time:
		return 1
	case t.node < o.node:
		return -1
	case t.node > o.node:
		return 1
	default:
		return 0
	}
}

func maxTS(set map[timestamp]struct{}) timestamp {
	first := true
	var max timestamp
	for ts := range set {
		if first || ts.compare(max) > 0 {
			max = ts
			first = false
		}
	}
	return max
}

type entry struct {
	adds    map[timestamp]struct{}
	removes map[timestamp]struct{}
	value   interface{}
	valueTS timestamp
	hasVal  bool
	child   *Map
}

func newEntry() *entry {
	return &entry{
		adds:    make(map[timestamp]struct{}),
		removes: make(map[timestamp]struct{}),
	}
}

// alive reports whether the key is present. Add-wins: the latest add only
// needs to be >= the latest remove.
func (e *entry) alive() bool {
	if len(e.adds) == 0 {
		return false
	}
	if len(e.removes) == 0 {
		return true
	}
	return maxTS(e.adds).compare(maxTS(e.removes)) >= 0
}

func (e *entry) clone() *entry {
	c := newEntry()
	for ts := range e.adds {
		c.adds[ts] = struct{}{}
	}
	for ts := range e.removes {
		c.removes[ts] = struct{}{}
	}
	c.value, c.valueTS, c.hasVal = e.value, e.valueTS, e.hasVal
	if e.child != nil {
		c.child = e.child.Clone()
	}
	return c
}

// Map is a replicated add-wins LWW map. A zero Map is not usable; use New.
// A Map is not safe for concurrent use by multiple goroutines; replicas are
// meant to be mutated on a single node and exchanged via Merge.
type Map struct {
	node    string
	clock   uint64
	entries map[string]*entry
}

// New returns an empty map replica owned by the given node ID. Node IDs must
// be unique across the distributed system.
func New(node string) *Map {
	return &Map{node: node, entries: make(map[string]*entry)}
}

func (m *Map) next() timestamp {
	m.clock++
	return timestamp{time: m.clock, node: m.node}
}

// Set writes value at the given path, creating intermediate sub-maps as
// needed. Every key along the path is re-added with the write timestamp, so
// a concurrent Remove of any ancestor key loses to this update.
func (m *Map) Set(value interface{}, path ...string) {
	if len(path) == 0 {
		panic("awlwwmap: Set requires at least one path element")
	}
	m.set(m.next(), value, path)
}

func (m *Map) set(ts timestamp, value interface{}, path []string) {
	e := m.entries[path[0]]
	if e == nil {
		e = newEntry()
		m.entries[path[0]] = e
	}
	e.adds[ts] = struct{}{}
	if len(path) == 1 {
		e.value, e.valueTS, e.hasVal = value, ts, true
		return
	}
	if e.child == nil {
		e.child = New(m.node)
	}
	e.child.set(ts, value, path[1:])
}

// Remove deletes the key at the given path. Removing a root key is
// Remove("key"); removing a nested field is Remove("key", "field").
// The removal is recorded as a tombstone and propagates via Merge.
func (m *Map) Remove(path ...string) {
	if len(path) == 0 {
		panic("awlwwmap: Remove requires at least one path element")
	}
	m.remove(m.next(), path)
}

func (m *Map) remove(ts timestamp, path []string) {
	e := m.entries[path[0]]
	if e == nil {
		e = newEntry()
		m.entries[path[0]] = e
	}
	if len(path) == 1 {
		e.removes[ts] = struct{}{}
		return
	}
	if e.child == nil {
		e.child = New(m.node)
	}
	e.child.remove(ts, path[1:])
}

// Get returns the leaf value stored at path, or false if the key is absent,
// removed, or is a sub-map rather than a leaf.
func (m *Map) Get(path ...string) (interface{}, bool) {
	if len(path) == 0 {
		return nil, false
	}
	e := m.entries[path[0]]
	if e == nil || !e.alive() {
		return nil, false
	}
	if len(path) == 1 {
		if !e.hasVal {
			return nil, false
		}
		return e.value, true
	}
	if e.child == nil {
		return nil, false
	}
	return e.child.Get(path[1:]...)
}

// Has reports whether the key at path is live (present).
func (m *Map) Has(path ...string) bool {
	if len(path) == 0 {
		return false
	}
	e := m.entries[path[0]]
	if e == nil || !e.alive() {
		return false
	}
	if len(path) == 1 {
		return true
	}
	if e.child == nil {
		return false
	}
	return e.child.Has(path[1:]...)
}

// Merge merges the state of another replica into this one. It is
// commutative, associative and idempotent, so replicas converge (CVRDT).
func (m *Map) Merge(other *Map) {
	if other.clock > m.clock {
		m.clock = other.clock
	}
	for key, oe := range other.entries {
		me := m.entries[key]
		if me == nil {
			m.entries[key] = oe.clone()
			continue
		}
		for ts := range oe.adds {
			me.adds[ts] = struct{}{}
		}
		for ts := range oe.removes {
			me.removes[ts] = struct{}{}
		}
		if oe.hasVal && (!me.hasVal || oe.valueTS.compare(me.valueTS) > 0) {
			me.value, me.valueTS, me.hasVal = oe.value, oe.valueTS, true
		}
		if oe.child != nil {
			if me.child == nil {
				me.child = New(m.node)
			}
			me.child.Merge(oe.child)
		}
	}
}

// Clone returns a deep copy of the replica (including its node ID and clock).
func (m *Map) Clone() *Map {
	c := New(m.node)
	c.clock = m.clock
	for key, e := range m.entries {
		c.entries[key] = e.clone()
	}
	return c
}

// Snapshot renders the live contents as plain nested Go maps. Removed keys
// and tombstoned fields are omitted.
func (m *Map) Snapshot() map[string]interface{} {
	out := make(map[string]interface{})
	for key, e := range m.entries {
		if !e.alive() {
			continue
		}
		if e.child != nil {
			out[key] = e.child.Snapshot()
		} else if e.hasVal {
			out[key] = e.value
		}
	}
	return out
}

// String returns a deterministic, human-readable rendering of Snapshot.
func (m *Map) String() string {
	var sb strings.Builder
	writeSnapshot(&sb, m.Snapshot(), 0)
	return sb.String()
}

func writeSnapshot(sb *strings.Builder, snap map[string]interface{}, depth int) {
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	indent := strings.Repeat("  ", depth)
	for _, k := range keys {
		if sub, ok := snap[k].(map[string]interface{}); ok {
			fmt.Fprintf(sb, "%s%s:\n", indent, k)
			writeSnapshot(sb, sub, depth+1)
		} else {
			fmt.Fprintf(sb, "%s%s: %v\n", indent, k, snap[k])
		}
	}
}

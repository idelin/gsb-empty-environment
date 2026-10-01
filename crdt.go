// Package crdt implements a composable JSON CRDT with arbitrarily nested
// Map, Array and Register nodes. Concurrent edits at different levels of
// the hierarchy merge precisely (three-way semantics via a state-based
// join), and merging never replaces the in-memory identity of a node that
// both replicas still share: shared subtrees are merged in place.
package crdt

import (
	"encoding/json"
	"sort"
)

// Timestamp is a Lamport clock pair identifying a single write.
type Timestamp struct {
	Counter uint64
	Replica string
}

// Less reports whether t orders before o (counter first, replica as tiebreak).
func (t Timestamp) Less(o Timestamp) bool {
	if t.Counter != o.Counter {
		return t.Counter < o.Counter
	}
	return t.Replica < o.Replica
}

// ElemID uniquely identifies an array element across replicas.
type ElemID struct {
	Counter uint64
	Replica string
}

func (id ElemID) Less(o ElemID) bool {
	if id.Counter != o.Counter {
		return id.Counter < o.Counter
	}
	return id.Replica < o.Replica
}

// Node is any CRDT node: *Map, *Array or *Register.
type Node interface {
	// mergeFrom joins other's state into the receiver, in place.
	mergeFrom(other Node)
	// clone deep-copies the node (without a Doc binding).
	clone() Node
	// attach (re)binds the subtree to a Doc so mutations get timestamps.
	attach(d *Doc)
	// value renders the node as plain JSON-able Go data.
	value() any
}

// Doc is one replica of the collaborative document.
type Doc struct {
	replica string
	clock   uint64
	root    *Map
}

// NewDoc creates an empty document whose root is a Map.
func NewDoc(replica string) *Doc {
	d := &Doc{replica: replica}
	d.root = &Map{entries: map[string]*mapEntry{}}
	d.root.attach(d)
	return d
}

// Root returns the document's root Map.
func (d *Doc) Root() *Map { return d.root }

func (d *Doc) next() Timestamp {
	d.clock++
	return Timestamp{Counter: d.clock, Replica: d.replica}
}

// Merge joins other's state into d. It is commutative, associative and
// idempotent; merging both ways makes two replicas converge.
func (d *Doc) Merge(other *Doc) {
	if other == nil || other == d {
		return
	}
	d.root.mergeFrom(other.root)
	if other.clock > d.clock {
		d.clock = other.clock
	}
}

// Fork creates an independent replica (new replica ID) starting from an
// exact copy of d's current state — the classic "shared history" setup
// for concurrent editing.
func (d *Doc) Fork(replica string) *Doc {
	nd := &Doc{replica: replica, clock: d.clock}
	nd.root = d.root.clone().(*Map)
	nd.root.attach(nd)
	return nd
}

// Value renders the whole document as plain Go data (map[string]any,
// []any, scalars) suitable for json.Marshal.
func (d *Doc) Value() any { return d.root.value() }

// ToJSON marshals the document's current value.
func (d *Doc) ToJSON() ([]byte, error) {
	return json.MarshalIndent(d.Value(), "", "  ")
}

// ---------------------------------------------------------------- Map

type mapEntry struct {
	node    Node
	ts      Timestamp // timestamp of the write that created/replaced/deleted the key
	deleted bool
}

// Map is a LWW-map keyed by string. Keys written independently on two
// replicas resolve by last-writer-wins; keys descending from the same
// write merge their subtrees recursively (in place).
type Map struct {
	doc     *Doc
	entries map[string]*mapEntry
}

// Get returns the live node at key, or nil.
func (m *Map) Get(key string) Node {
	if e, ok := m.entries[key]; ok && !e.deleted {
		return e.node
	}
	return nil
}

// Keys returns the live keys in sorted order.
func (m *Map) Keys() []string {
	var keys []string
	for k, e := range m.entries {
		if !e.deleted {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (m *Map) put(key string, n Node) Node {
	ts := m.doc.next()
	n.attach(m.doc)
	if r, ok := n.(*Register); ok {
		r.ts = ts
	}
	m.entries[key] = &mapEntry{node: n, ts: ts}
	return n
}

// PutMap stores a new empty Map at key and returns it.
func (m *Map) PutMap(key string) *Map {
	return m.put(key, &Map{entries: map[string]*mapEntry{}}).(*Map)
}

// PutArray stores a new empty Array at key and returns it.
func (m *Map) PutArray(key string) *Array {
	return m.put(key, &Array{}).(*Array)
}

// PutRegister stores a Register holding v at key and returns it.
func (m *Map) PutRegister(key string, v any) *Register {
	return m.put(key, &Register{val: v}).(*Register)
}

// Delete tombstones key (a write that participates in LWW).
func (m *Map) Delete(key string) {
	m.entries[key] = &mapEntry{ts: m.doc.next(), deleted: true}
}

func (m *Map) mergeFrom(other Node) {
	om, ok := other.(*Map)
	if !ok {
		return
	}
	for key, oe := range om.entries {
		me, exists := m.entries[key]
		switch {
		case !exists:
			m.entries[key] = cloneEntry(oe, m.doc)
		case oe.ts == me.ts:
			// Same originating write: merge shared subtree in place so
			// existing Go pointers to me.node stay valid.
			if !me.deleted && !oe.deleted {
				me.node.mergeFrom(oe.node)
			}
		case me.ts.Less(oe.ts):
			// Their write of the key is newer (incl. deletes): take it.
			m.entries[key] = cloneEntry(oe, m.doc)
		}
	}
}

func cloneEntry(e *mapEntry, d *Doc) *mapEntry {
	ne := &mapEntry{ts: e.ts, deleted: e.deleted}
	if e.node != nil {
		ne.node = e.node.clone()
		ne.node.attach(d)
	}
	return ne
}

func (m *Map) clone() Node {
	nm := &Map{entries: make(map[string]*mapEntry, len(m.entries))}
	for k, e := range m.entries {
		ne := &mapEntry{ts: e.ts, deleted: e.deleted}
		if e.node != nil {
			ne.node = e.node.clone()
		}
		nm.entries[k] = ne
	}
	return nm
}

func (m *Map) attach(d *Doc) {
	m.doc = d
	for _, e := range m.entries {
		if e.node != nil {
			e.node.attach(d)
		}
	}
}

func (m *Map) value() any {
	out := make(map[string]any, len(m.entries))
	for k, e := range m.entries {
		if !e.deleted {
			out[k] = e.node.value()
		}
	}
	return out
}

// ---------------------------------------------------------------- Array

type elem struct {
	id      ElemID
	prev    ElemID // id of the element it was inserted after (zero = head)
	node    Node
	deleted bool
}

// Array is an RGA (Replicated Growable Array) of nodes. Elements carry
// unique IDs; concurrent inserts merge deterministically, deletes are
// tombstones, and shared elements merge their payloads in place.
type Array struct {
	doc   *Doc
	elems []*elem
}

// Len returns the number of live elements.
func (a *Array) Len() int {
	n := 0
	for _, e := range a.elems {
		if !e.deleted {
			n++
		}
	}
	return n
}

// At returns the i-th live element's node, or nil if out of range.
func (a *Array) At(i int) Node {
	n := 0
	for _, e := range a.elems {
		if e.deleted {
			continue
		}
		if n == i {
			return e.node
		}
		n++
	}
	return nil
}

func (a *Array) push(n Node) Node {
	ts := a.doc.next()
	n.attach(a.doc)
	if r, ok := n.(*Register); ok {
		r.ts = ts
	}
	var prev ElemID
	if len(a.elems) > 0 {
		prev = a.elems[len(a.elems)-1].id
	}
	a.elems = append(a.elems, &elem{
		id:   ElemID{Counter: ts.Counter, Replica: ts.Replica},
		prev: prev,
		node: n,
	})
	return n
}

// PushMap appends a new empty Map and returns it.
func (a *Array) PushMap() *Map {
	return a.push(&Map{entries: map[string]*mapEntry{}}).(*Map)
}

// PushArray appends a new empty Array and returns it.
func (a *Array) PushArray() *Array {
	return a.push(&Array{}).(*Array)
}

// PushRegister appends a Register holding v and returns it.
func (a *Array) PushRegister(v any) *Register {
	return a.push(&Register{val: v}).(*Register)
}

// Delete tombstones the i-th live element.
func (a *Array) Delete(i int) bool {
	n := 0
	for _, e := range a.elems {
		if e.deleted {
			continue
		}
		if n == i {
			e.deleted = true
			return true
		}
		n++
	}
	return false
}

func (a *Array) mergeFrom(other Node) {
	oa, ok := other.(*Array)
	if !ok {
		return
	}
	index := make(map[ElemID]*elem, len(a.elems))
	for _, e := range a.elems {
		index[e.id] = e
	}
	for _, oe := range oa.elems {
		if e, ok := index[oe.id]; ok {
			// Shared element: union tombstones, merge payload in place.
			e.deleted = e.deleted || oe.deleted
			e.node.mergeFrom(oe.node)
		} else {
			c := &elem{id: oe.id, prev: oe.prev, deleted: oe.deleted, node: oe.node.clone()}
			c.node.attach(a.doc)
			a.elems = append(a.elems, c)
			index[oe.id] = c
		}
	}
	a.reorder()
}

// reorder rebuilds the sequence from prev-links, RGA-style: siblings
// (elements inserted after the same prev) order by descending ID.
func (a *Array) reorder() {
	children := make(map[ElemID][]*elem, len(a.elems))
	for _, e := range a.elems {
		children[e.prev] = append(children[e.prev], e)
	}
	for _, group := range children {
		sort.Slice(group, func(i, j int) bool { return group[j].id.Less(group[i].id) })
	}
	ordered := make([]*elem, 0, len(a.elems))
	var walk func(prev ElemID)
	walk = func(prev ElemID) {
		for _, e := range children[prev] {
			ordered = append(ordered, e)
			walk(e.id)
		}
	}
	walk(ElemID{})
	a.elems = ordered
}

func (a *Array) clone() Node {
	na := &Array{elems: make([]*elem, len(a.elems))}
	for i, e := range a.elems {
		na.elems[i] = &elem{id: e.id, prev: e.prev, deleted: e.deleted, node: e.node.clone()}
	}
	return na
}

func (a *Array) attach(d *Doc) {
	a.doc = d
	for _, e := range a.elems {
		e.node.attach(d)
	}
}

func (a *Array) value() any {
	out := make([]any, 0, len(a.elems))
	for _, e := range a.elems {
		if !e.deleted {
			out = append(out, e.node.value())
		}
	}
	return out
}

// ---------------------------------------------------------------- Register

// Register is a last-writer-wins cell for a scalar JSON value.
type Register struct {
	doc *Doc
	val any
	ts  Timestamp
}

// Set overwrites the value, recording a fresh timestamp.
func (r *Register) Set(v any) {
	r.val = v
	r.ts = r.doc.next()
}

// Value returns the current value.
func (r *Register) Value() any { return r.val }

func (r *Register) mergeFrom(other Node) {
	or, ok := other.(*Register)
	if !ok {
		return
	}
	if r.ts.Less(or.ts) {
		r.val = or.val
		r.ts = or.ts
	}
}

func (r *Register) clone() Node { return &Register{val: r.val, ts: r.ts} }

func (r *Register) attach(d *Doc) { r.doc = d }

func (r *Register) value() any { return r.val }

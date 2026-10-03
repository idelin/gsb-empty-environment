// Package awlwwmap implements an AW-LWW-Map: an Add-Wins,
// Last-Writer-Wins replicated map CRDT.
//
// The map stores root keys, each holding a dynamic set of sub-fields.
// Every write is tagged with a unique dot (node, seq). Removing a key or
// field records the set of dots it causally observed (a tombstone).
// On merge, a write survives a concurrent removal if its dot was not
// observed by that removal -- this is the "add-wins" rule: a concurrent
// update always beats a concurrent delete. Concurrent writes to the same
// field are resolved by last-writer-wins on (timestamp, node).
package awlwwmap

import (
	"sort"
	"sync"
	"time"
)

// Dot uniquely identifies a single write across all replicas.
type Dot struct {
	Node string
	Seq  uint64
}

// fieldVersion is one LWW version of a field value.
type fieldVersion struct {
	Value interface{}
	TS    int64
	Node  string
	Dot   Dot
}

// keyEntry holds the live fields and the tombstones of one root key.
type keyEntry struct {
	fields       map[string]fieldVersion
	fieldTombs   map[string]map[Dot]struct{}
	keyTombstone map[Dot]struct{}
}

func newKeyEntry() *keyEntry {
	return &keyEntry{
		fields:       make(map[string]fieldVersion),
		fieldTombs:   make(map[string]map[Dot]struct{}),
		keyTombstone: make(map[Dot]struct{}),
	}
}

func (e *keyEntry) dead(field string, d Dot) bool {
	if _, ok := e.keyTombstone[d]; ok {
		return true
	}
	_, ok := e.fieldTombs[field][d]
	return ok
}

func (e *keyEntry) clone() *keyEntry {
	c := newKeyEntry()
	for f, v := range e.fields {
		c.fields[f] = v
	}
	for f, ds := range e.fieldTombs {
		s := make(map[Dot]struct{}, len(ds))
		for d := range ds {
			s[d] = struct{}{}
		}
		c.fieldTombs[f] = s
	}
	for d := range e.keyTombstone {
		c.keyTombstone[d] = struct{}{}
	}
	return c
}

// Map is a replicated add-wins LWW map. It is safe for concurrent use.
type Map struct {
	mu   sync.Mutex
	node string
	seq  uint64
	now  func() int64
	keys map[string]*keyEntry
}

// New creates a replica owned by the given node ID.
func New(node string) *Map {
	return &Map{
		node: node,
		now:  func() int64 { return time.Now().UnixNano() },
		keys: make(map[string]*keyEntry),
	}
}

func (m *Map) entry(key string) *keyEntry {
	e, ok := m.keys[key]
	if !ok {
		e = newKeyEntry()
		m.keys[key] = e
	}
	return e
}

// Set writes field=value under key using the local clock.
func (m *Map) Set(key, field string, value interface{}) {
	m.SetAt(key, field, value, m.now())
}

// SetAt writes field=value under key with an explicit timestamp.
// Timestamps must be comparable across nodes (e.g. Unix nanos).
func (m *Map) SetAt(key, field string, value interface{}, ts int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.entry(key).fields[field] = fieldVersion{
		Value: value,
		TS:    ts,
		Node:  m.node,
		Dot:   Dot{Node: m.node, Seq: m.seq},
	}
}

// RemoveKey deletes a root key. The deletion only covers writes it has
// causally observed; concurrent updates on other replicas survive merge.
func (m *Map) RemoveKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entry(key)
	for _, fv := range e.fields {
		e.keyTombstone[fv.Dot] = struct{}{}
	}
	// Field-level tombstones are subsumed by the key tombstone.
	for _, ds := range e.fieldTombs {
		for d := range ds {
			e.keyTombstone[d] = struct{}{}
		}
	}
	e.fieldTombs = make(map[string]map[Dot]struct{})
	e.fields = make(map[string]fieldVersion)
}

// RemoveField deletes one sub-field of a key. Like RemoveKey, it only
// covers the currently observed version; concurrent updates survive.
func (m *Map) RemoveField(key, field string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entry(key)
	fv, ok := e.fields[field]
	if !ok {
		return
	}
	ds := e.fieldTombs[field]
	if ds == nil {
		ds = make(map[Dot]struct{})
		e.fieldTombs[field] = ds
	}
	ds[fv.Dot] = struct{}{}
	delete(e.fields, field)
}

// Get returns a snapshot of all live fields of key, and whether the key
// is present (i.e. has at least one live field).
func (m *Map) Get(key string) (map[string]interface{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.keys[key]
	if !ok || len(e.fields) == 0 {
		return nil, false
	}
	out := make(map[string]interface{}, len(e.fields))
	for f, fv := range e.fields {
		out[f] = fv.Value
	}
	return out, true
}

// GetField returns one field's value and whether it is present.
func (m *Map) GetField(key, field string) (interface{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.keys[key]
	if !ok {
		return nil, false
	}
	fv, ok := e.fields[field]
	if !ok {
		return nil, false
	}
	return fv.Value, true
}

// Keys returns the sorted list of live root keys.
func (m *Map) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k, e := range m.keys {
		if len(e.fields) > 0 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Merge merges the state of other into m. It is associative, commutative
// and idempotent; replicas that exchange merges converge.
func (m *Map) Merge(other *Map) {
	if m == other {
		return
	}
	// Snapshot under other's lock, then apply under m's lock. This
	// ordering avoids deadlock when merges run in both directions.
	other.mu.Lock()
	snapshot := make(map[string]*keyEntry, len(other.keys))
	for k, e := range other.keys {
		snapshot[k] = e.clone()
	}
	other.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	for key, oe := range snapshot {
		e := m.entry(key)
		for d := range oe.keyTombstone {
			e.keyTombstone[d] = struct{}{}
		}
		for f, ds := range oe.fieldTombs {
			dst := e.fieldTombs[f]
			if dst == nil {
				dst = make(map[Dot]struct{})
				e.fieldTombs[f] = dst
			}
			for d := range ds {
				dst[d] = struct{}{}
			}
		}
		// Drop local writes observed by the incoming tombstones.
		for f, fv := range e.fields {
			if e.dead(f, fv.Dot) {
				delete(e.fields, f)
			}
		}
		// Add-wins: incoming writes not covered by any tombstone survive.
		// LWW: per field, the higher (TS, Node) version wins.
		for f, ofv := range oe.fields {
			if e.dead(f, ofv.Dot) {
				continue
			}
			lv, ok := e.fields[f]
			if !ok || lwwLess(lv, ofv) {
				e.fields[f] = ofv
			}
		}
	}
}

// lwwLess reports whether version a loses to version b under LWW.
func lwwLess(a, b fieldVersion) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.Node < b.Node
}

// Package crdt implements a causal-tree (CT) conflict-free replicated
// data type for collaborative text editing.
//
// Every character is a node carrying a unique Lamport timestamp and an
// explicit pointer to its parent character node. Concurrent inserts that
// share the same parent are ordered deterministically by (clock, site),
// so all replicas converge to the identical document after syncing.
package crdt

import (
	"fmt"
	"sort"
	"strings"
)

// ID uniquely identifies a character node. It is a Lamport timestamp
// (Clock) disambiguated by the originating replica (Site).
type ID struct {
	Clock uint64
	Site  string
}

// Less defines the total, deterministic order over IDs: first by Lamport
// clock, then by site ID. It is the tie-breaker that makes concurrent
// sibling inserts converge identically on every replica.
func (id ID) Less(other ID) bool {
	if id.Clock != other.Clock {
		return id.Clock < other.Clock
	}
	return id.Site < other.Site
}

func (id ID) String() string { return fmt.Sprintf("%d@%s", id.Clock, id.Site) }

// rootID is the implicit document root; every first character points to it.
var rootID = ID{}

// Node is a single character in the causal tree.
type Node struct {
	ID     ID   // unique Lamport timestamp of this character
	Parent ID   // explicit pointer to the parent character (rootID for the first char)
	Value  rune // the character itself
}

// Tree is one replica of the collaborative document.
type Tree struct {
	site  string
	clock uint64

	nodes    map[ID]Node
	children map[ID][]ID // parent -> children, kept sorted by ID order
	pending  map[ID]Node // nodes whose parent has not arrived yet
}

// New creates an empty replica owned by the given site (client) ID.
func New(site string) *Tree {
	return &Tree{
		site:     site,
		nodes:    make(map[ID]Node),
		children: make(map[ID][]ID),
		pending:  make(map[ID]Node),
	}
}

// Site returns the replica's site ID.
func (t *Tree) Site() string { return t.site }

// RootID is the parent pointer used for inserting at the very beginning
// of the document.
func RootID() ID { return rootID }

// Insert appends value after the character identified by parent and
// returns the new node's ID. Use RootID() to insert at the start.
func (t *Tree) Insert(parent ID, value rune) (Node, error) {
	if parent != rootID {
		if _, ok := t.nodes[parent]; !ok {
			return Node{}, fmt.Errorf("crdt: unknown parent %v", parent)
		}
	}
	t.clock++
	n := Node{ID: ID{Clock: t.clock, Site: t.site}, Parent: parent, Value: value}
	t.integrate(n)
	return n, nil
}

// Apply integrates a node received from a remote replica. Nodes whose
// parent has not arrived yet are buffered and integrated once possible,
// so delivery order does not affect the converged result.
func (t *Tree) Apply(n Node) {
	if _, ok := t.nodes[n.ID]; ok {
		return // idempotent: already known
	}
	if n.Parent != rootID {
		if _, ok := t.nodes[n.Parent]; !ok {
			t.pending[n.ID] = n
			return
		}
	}
	t.integrate(n)
}

// Merge applies every node of other (including ones still pending there)
// and returns after both replicas hold the same causal tree.
func (t *Tree) Merge(other *Tree) {
	for _, n := range other.Nodes() {
		t.Apply(n)
	}
}

// Nodes returns all nodes in the tree in deterministic ID order.
func (t *Tree) Nodes() []Node {
	out := make([]Node, 0, len(t.nodes))
	for _, n := range t.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Less(out[j].ID) })
	return out
}

// integrate inserts n into the tree and then drains any buffered nodes
// that were waiting on it (recursively).
func (t *Tree) integrate(n Node) {
	t.nodes[n.ID] = n
	if n.ID.Clock > t.clock {
		t.clock = n.ID.Clock // Lamport clock catches up
	}
	siblings := t.children[n.Parent]
	i := sort.Search(len(siblings), func(i int) bool {
		return n.ID.Less(siblings[i])
	})
	siblings = append(siblings, ID{})
	copy(siblings[i+1:], siblings[i:])
	siblings[i] = n.ID
	t.children[n.Parent] = siblings

	// Drain nodes that were waiting for this parent.
	for id, p := range t.pending {
		if p.Parent == n.ID {
			delete(t.pending, id)
			t.integrate(p)
		}
	}
}

// String renders the document by a depth-first walk from the root.
// Children of each node are visited in deterministic (clock, site) order,
// which is exactly what makes concurrent sibling inserts converge.
func (t *Tree) String() string {
	var b strings.Builder
	var walk func(parent ID)
	walk = func(parent ID) {
		for _, id := range t.children[parent] {
			b.WriteRune(t.nodes[id].Value)
			walk(id)
		}
	}
	walk(rootID)
	return b.String()
}

// Len returns the number of characters in the document.
func (t *Tree) Len() int { return len(t.nodes) }

// Package undotree implements a reversible editing undo tree (Undo/Redo CRDT
// graph) for collaborative editing.
//
// Every edit is an immutable node in an operation graph. Undo and Redo do not
// remove nodes; they append inverse "compensation" nodes (Undo/Redo ops) that
// reference the target operation, so the graph stays topologically valid and
// concurrent collaborators' input is never destroyed. Replicas converge by
// exchanging and merging operation logs (a last-writer-free, order-independent
// CRDT over the op set, rendered with an RGA sequence ordering).
package undotree

import (
	"fmt"
	"sort"
	"strings"
)

// CharID uniquely identifies a character across all replicas.
type CharID struct {
	Author string
	Seq    int
}

// IsRoot reports whether id refers to the virtual document root.
func (id CharID) IsRoot() bool { return id.Seq == 0 && id.Author == "" }

// OpID uniquely identifies an operation node in the undo graph.
type OpID struct {
	Author string
	Seq    int
}

// OpKind is the type of an operation node.
type OpKind int

const (
	// OpInsert inserts a run of characters (one node per user edit).
	OpInsert OpKind = iota
	// OpUndo is a compensation node that deactivates its target insert.
	OpUndo
	// OpRedo is a compensation node that reactivates its target insert.
	OpRedo
)

func (k OpKind) String() string {
	switch k {
	case OpInsert:
		return "Insert"
	case OpUndo:
		return "Undo"
	case OpRedo:
		return "Redo"
	}
	return "Unknown"
}

// Char is a single inserted character, linked to its left neighbour (parent)
// to form an RGA sequence.
type Char struct {
	ID     CharID
	Parent CharID
	Value  rune
}

// Op is a node in the undo/redo CRDT graph.
type Op struct {
	ID      OpID
	Lamport int
	Author  string
	Kind    OpKind
	Chars   []Char // payload for OpInsert
	Target  OpID   // referenced node for OpUndo / OpRedo
}

func (op Op) String() string {
	switch op.Kind {
	case OpInsert:
		var b strings.Builder
		for _, c := range op.Chars {
			b.WriteRune(c.Value)
		}
		return fmt.Sprintf("%s#%d %s(%q)@%d", op.ID.Author, op.ID.Seq, op.Kind, b.String(), op.Lamport)
	default:
		return fmt.Sprintf("%s#%d %s(%s#%d)@%d", op.ID.Author, op.ID.Seq, op.Kind, op.Target.Author, op.Target.Seq, op.Lamport)
	}
}

// Doc is one replica of the collaborative document.
type Doc struct {
	site     string
	clock    int // Lamport clock
	opSeq    int // per-site operation sequence
	charSeq  int // per-site character sequence
	ops      []Op
	opSet    map[OpID]bool
	charToOp map[CharID]OpID
}

// NewDoc creates a replica owned by site.
func NewDoc(site string) *Doc {
	return &Doc{
		site:     site,
		opSet:    make(map[OpID]bool),
		charToOp: make(map[CharID]OpID),
	}
}

// Insert appends an insert node for text whose first character is linked
// after parent (zero CharID inserts at the document start). The whole text
// forms a single undoable operation node.
func (d *Doc) Insert(parent CharID, text string) Op {
	chars := make([]Char, 0, len(text))
	prev := parent
	for _, r := range text {
		d.charSeq++
		c := Char{ID: CharID{Author: d.site, Seq: d.charSeq}, Parent: prev, Value: r}
		chars = append(chars, c)
		prev = c.ID
	}
	op := d.newNode(OpInsert)
	op.Chars = chars
	d.Apply(op)
	return op
}

// Undo appends a compensation node that retracts the author's most recent
// still-active insert. It reports false when there is nothing to undo.
func (d *Doc) Undo() (Op, bool) {
	active := d.activeOps()
	for i := len(d.ops) - 1; i >= 0; i-- {
		op := d.ops[i]
		if op.Kind == OpInsert && op.Author == d.site && active[op.ID] {
			n := d.newNode(OpUndo)
			n.Target = op.ID
			d.Apply(n)
			return n, true
		}
	}
	return Op{}, false
}

// Redo appends a compensation node that restores the author's most recently
// undone insert. It reports false when there is nothing to redo.
func (d *Doc) Redo() (Op, bool) {
	active := d.activeOps()
	for i := len(d.ops) - 1; i >= 0; i-- {
		op := d.ops[i]
		if op.Kind == OpUndo && op.Author == d.site && !active[op.Target] {
			n := d.newNode(OpRedo)
			n.Target = op.Target
			d.Apply(n)
			return n, true
		}
	}
	return Op{}, false
}

// Apply integrates an operation node (local or remote) into the graph.
// It is idempotent: re-applying the same node is a no-op.
func (d *Doc) Apply(op Op) {
	if d.opSet[op.ID] {
		return
	}
	d.opSet[op.ID] = true
	d.ops = append(d.ops, op)
	sort.SliceStable(d.ops, func(i, j int) bool { return lessOp(d.ops[i], d.ops[j]) })
	if op.Lamport > d.clock {
		d.clock = op.Lamport
	}
	if op.Kind == OpInsert {
		for _, c := range op.Chars {
			d.charToOp[c.ID] = op.ID
		}
	}
}

// Merge pulls every operation node from other, making both replicas converge
// to the same graph (and therefore the same text).
func (d *Doc) Merge(other *Doc) {
	for _, op := range other.ops {
		d.Apply(op)
	}
}

// Ops returns the graph nodes in causal (Lamport) order.
func (d *Doc) Ops() []Op {
	out := make([]Op, len(d.ops))
	copy(out, d.ops)
	return out
}

// Text renders the currently visible document content.
func (d *Doc) Text() string {
	active := d.activeOps()
	children := make(map[CharID][]Char)
	for _, op := range d.ops {
		if op.Kind != OpInsert {
			continue
		}
		for _, c := range op.Chars {
			children[c.Parent] = append(children[c.Parent], c)
		}
	}
	// RGA: siblings sorted by descending ID, so concurrent inserts at the same
	// position get a deterministic, convergent order.
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			a, b := children[parent][i].ID, children[parent][j].ID
			if a.Seq != b.Seq {
				return a.Seq > b.Seq
			}
			return a.Author > b.Author
		})
	}
	var out []rune
	var walk func(parent CharID)
	walk = func(parent CharID) {
		for _, c := range children[parent] {
			if active[d.charToOp[c.ID]] {
				out = append(out, c.Value)
			}
			// Children stay reachable even when their own node is undone,
			// which keeps concurrent collaborators' anchors valid.
			walk(c.ID)
		}
	}
	walk(CharID{})
	return string(out)
}

// LastChar returns the ID of the last character of op, usable as the parent
// anchor for a subsequent insert.
func LastChar(op Op) CharID {
	if len(op.Chars) == 0 {
		return CharID{}
	}
	return op.Chars[len(op.Chars)-1].ID
}

func (d *Doc) newNode(kind OpKind) Op {
	d.clock++
	d.opSeq++
	return Op{
		ID:      OpID{Author: d.site, Seq: d.opSeq},
		Lamport: d.clock,
		Author:  d.site,
		Kind:    kind,
	}
}

// activeOps replays the graph to compute which insert nodes are currently
// effective. Undo/Redo compensation nodes flip their target's state.
func (d *Doc) activeOps() map[OpID]bool {
	active := make(map[OpID]bool)
	for _, op := range d.ops {
		switch op.Kind {
		case OpInsert:
			active[op.ID] = true
		case OpUndo:
			active[op.Target] = false
		case OpRedo:
			active[op.Target] = true
		}
	}
	return active
}

func lessOp(a, b Op) bool {
	if a.Lamport != b.Lamport {
		return a.Lamport < b.Lamport
	}
	if a.ID.Author != b.ID.Author {
		return a.ID.Author < b.ID.Author
	}
	return a.ID.Seq < b.ID.Seq
}

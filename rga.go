// Package rga implements a Replicated Growable Array (RGA), a CRDT for
// collaborative text editing. Characters are organized as linked-list
// nodes; each node records a globally unique ID and the ID of its causal
// predecessor (parent). Concurrent inserts at the same parent are ordered
// by node ID in descending order, which makes all replicas converge to the
// same sequence regardless of the order in which operations arrive.
package rga

import (
	"fmt"
	"strings"
)

// ID uniquely identifies a node across all replicas. The zero value is the
// ID of the implicit root sentinel, used as the parent of the first
// character of the document.
type ID struct {
	Site int // replica (site) that created the node
	Seq  int // Lamport clock value, always greater than the parent's Seq
}

// RootID is the parent ID to use when inserting at the very beginning of
// the document.
var RootID = ID{}

// Compare returns -1, 0 or +1 depending on whether a orders before, equal
// to, or after b. It defines a total order over all node IDs.
func (a ID) Compare(b ID) int {
	switch {
	case a.Seq != b.Seq:
		if a.Seq < b.Seq {
			return -1
		}
		return 1
	case a.Site != b.Site:
		if a.Site < b.Site {
			return -1
		}
		return 1
	default:
		return 0
	}
}

// Op is an insert operation. It is the unit of replication: applying the
// same set of Ops to any replica (in causal order) yields the same
// document.
type Op struct {
	ID     ID   // unique ID of the new node
	Parent ID   // ID of the causal predecessor the character is inserted after
	Ch     rune // the inserted character
}

// node is a single character in the linked list.
type node struct {
	id   ID
	ch   rune
	next *node
}

// RGA is a single replica of the replicated growable array.
type RGA struct {
	site  int
	clock int   // Lamport clock: max Seq seen so far
	root  *node // sentinel head, never holds a character
	index map[ID]*node
	log   []Op // every op applied to this replica, in apply order
}

// New creates an empty replica for the given site. Site IDs must be unique
// per replica and greater than zero.
func New(site int) *RGA {
	root := &node{id: RootID}
	return &RGA{
		site:  site,
		root:  root,
		index: map[ID]*node{RootID: root},
	}
}

// InsertAfter inserts ch immediately after the node identified by parent
// (use RootID to insert at the head) and returns the generated operation,
// which must be broadcast to the other replicas.
func (r *RGA) InsertAfter(parent ID, ch rune) (Op, error) {
	p, ok := r.index[parent]
	if !ok {
		return Op{}, fmt.Errorf("rga: unknown parent %v", parent)
	}
	// The new node's Seq must exceed its parent's, so that a child always
	// orders after its causal predecessor in the total ID order.
	if p.id.Seq > r.clock {
		r.clock = p.id.Seq
	}
	r.clock++
	op := Op{ID: ID{Site: r.site, Seq: r.clock}, Parent: parent, Ch: ch}
	r.apply(op)
	return op, nil
}

// Apply integrates a remote operation. It is idempotent: applying the same
// op twice is a no-op. It returns an error if the op's causal predecessor
// has not been applied yet (ops must be delivered in causal order; see
// Merge for an unordered variant).
func (r *RGA) Apply(op Op) error {
	if _, ok := r.index[op.ID]; ok {
		return nil // already applied
	}
	if _, ok := r.index[op.Parent]; !ok {
		return fmt.Errorf("rga: missing causal predecessor %v for op %v", op.Parent, op.ID)
	}
	r.apply(op)
	return nil
}

// Merge integrates every operation known to other that this replica is
// missing, regardless of the order in which they were originally applied.
func (r *RGA) Merge(other *RGA) {
	pending := make([]Op, 0, len(other.log))
	pending = append(pending, other.log...)
	for len(pending) > 0 {
		rest := pending[:0]
		progress := false
		for _, op := range pending {
			if _, ok := r.index[op.ID]; ok {
				continue // already have it
			}
			if _, ok := r.index[op.Parent]; ok {
				r.apply(op)
				progress = true
			} else {
				rest = append(rest, op)
			}
		}
		if !progress && len(rest) == len(pending) {
			panic("rga: merge cannot make progress, op log is corrupted")
		}
		pending = rest
	}
}

// apply inserts op's node into the linked list. Among siblings (nodes
// sharing the same causal predecessor) nodes are kept in descending ID
// order: the insertion walk skips over every node whose ID is greater than
// the new one, so concurrent inserts at the same position are ordered
// deterministically on every replica.
func (r *RGA) apply(op Op) {
	parent := r.index[op.Parent]
	if op.ID.Seq > r.clock {
		r.clock = op.ID.Seq
	}
	n := &node{id: op.ID, ch: op.Ch}
	cur := parent
	for cur.next != nil && cur.next.id.Compare(op.ID) > 0 {
		cur = cur.next
	}
	n.next = cur.next
	cur.next = n
	r.index[op.ID] = n
	r.log = append(r.log, op)
}

// String returns the visible text of the document.
func (r *RGA) String() string {
	var sb strings.Builder
	for n := r.root.next; n != nil; n = n.next {
		sb.WriteRune(n.ch)
	}
	return sb.String()
}

// Len returns the number of visible characters.
func (r *RGA) Len() int {
	n := 0
	for cur := r.root.next; cur != nil; cur = cur.next {
		n++
	}
	return n
}

// IDAt returns the ID of the i-th visible character (0-based). Passing the
// result as the parent of InsertAfter inserts right after that character.
func (r *RGA) IDAt(i int) (ID, error) {
	if i < 0 {
		return ID{}, fmt.Errorf("rga: index %d out of range", i)
	}
	cur := r.root
	for j := 0; j <= i; j++ {
		cur = cur.next
		if cur == nil {
			return ID{}, fmt.Errorf("rga: index %d out of range", i)
		}
	}
	return cur.id, nil
}

// Ops returns a copy of every operation applied to this replica, in apply
// order. It can be replayed on another replica with Apply or Merge.
func (r *RGA) Ops() []Op {
	ops := make([]Op, len(r.log))
	copy(ops, r.log)
	return ops
}

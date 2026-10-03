// Package rga implements a Replicated Growable Array (RGA), a CRDT for
// collaborative text editing based on a causal tree.
//
// Each character is stored as a linked-list node carrying a globally unique,
// totally ordered ID and a reference to its causal predecessor (the node it
// was inserted after). Nodes form a tree: a node's children are the
// characters inserted directly after it. When several replicas insert
// concurrently at the same position, the siblings are ordered by ID in
// descending order, which makes all replicas converge to the same sequence
// without interleaving characters from concurrent runs.
package rga

import (
	"slices"
	"strings"
)

// ID uniquely identifies a node across all replicas. IDs are totally
// ordered: first by sequence number, then by site.
type ID struct {
	Seq  int
	Site string
}

// Compare returns -1, 0 or 1 depending on whether a orders before, equal to,
// or after b.
func (a ID) Compare(b ID) int {
	switch {
	case a.Seq != b.Seq:
		if a.Seq < b.Seq {
			return -1
		}
		return 1
	case a.Site != b.Site:
		return strings.Compare(a.Site, b.Site)
	}
	return 0
}

func (a ID) String() string { return a.Site + ":" + itoa(a.Seq) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// node is one character in the causal tree.
type node struct {
	id       ID
	value    rune
	deleted  bool // tombstone
	children []*node
}

// Op is a commutative edit operation that can be broadcast to other
// replicas. Delivery may be out of order; Apply buffers operations until
// their causal dependencies arrive.
type Op struct {
	ID    ID   // ID of the new node (insert) or target node (delete)
	Prev  ID   // causal predecessor for inserts; zero ID means the head
	Value rune // inserted character
	Del   bool // true: delete the node identified by ID
}

// Entry is a visible (non-deleted) character together with its ID, in
// document order.
type Entry struct {
	ID    ID
	Value rune
}

// RGA is a single replica of the replicated growable array.
type RGA struct {
	site    string
	seq     int
	root    *node // sentinel head, never rendered
	index   map[ID]*node
	pending []Op
}

// New creates an empty replica owned by the given site. The site name must
// be unique across all replicas so that generated IDs are unique.
func New(site string) *RGA {
	r := &RGA{site: site, index: make(map[ID]*node)}
	r.root = &node{id: ID{}}
	r.index[r.root.id] = r.root
	return r
}

// nextID allocates a fresh ID for a local insert. The sequence number is a
// Lamport clock: it exceeds every ID the replica has observed, so a new
// insert always orders ahead of siblings its author already knew about.
func (r *RGA) nextID() ID {
	r.seq++
	return ID{Seq: r.seq, Site: r.site}
}

// observe advances the Lamport clock past an ID seen on the wire.
func (r *RGA) observe(id ID) {
	if id.Seq > r.seq {
		r.seq = id.Seq
	}
}

// InsertAfter inserts value after the node identified by prev (use the zero
// ID to insert at the very beginning) and returns the operation to
// broadcast to other replicas.
func (r *RGA) InsertAfter(prev ID, value rune) Op {
	op := Op{ID: r.nextID(), Prev: prev, Value: value}
	r.mustIntegrate(op)
	return op
}

// InsertAt inserts value so that it becomes the character at the given
// visible index and returns the operation to broadcast.
func (r *RGA) InsertAt(index int, value rune) Op {
	var prev ID
	if index > 0 {
		prev = r.Entries()[index-1].ID
	}
	return r.InsertAfter(prev, value)
}

// Delete removes the node with the given ID (leaving a tombstone) and
// returns the operation to broadcast.
func (r *RGA) Delete(id ID) Op {
	op := Op{ID: id, Del: true}
	r.mustIntegrate(op)
	return op
}

// Apply integrates a remote operation. Operations whose causal predecessor
// has not arrived yet are buffered and retried once dependencies land.
func (r *RGA) Apply(op Op) {
	if !r.integrate(op) {
		r.pending = append(r.pending, op)
	}
	r.flush()
}

// mustIntegrate integrates a locally generated operation, whose causal
// dependencies are guaranteed to be present.
func (r *RGA) mustIntegrate(op Op) {
	if !r.integrate(op) {
		panic("rga: local operation has missing causal predecessor")
	}
}

// integrate applies op, reporting whether its dependencies were satisfied.
func (r *RGA) integrate(op Op) bool {
	if op.Del {
		target, ok := r.index[op.ID]
		if !ok {
			return false
		}
		target.deleted = true
		r.observe(op.ID)
		return true
	}
	if _, dup := r.index[op.ID]; dup {
		return true // idempotent re-delivery
	}
	parent, ok := r.index[op.Prev]
	if !ok {
		return false
	}
	child := &node{id: op.ID, value: op.Value}
	// Children are kept sorted by ID in descending order, so concurrent
	// inserts at the same position resolve deterministically on every
	// replica.
	pos, _ := slices.BinarySearchFunc(parent.children, child, func(a, b *node) int {
		return b.id.Compare(a.id)
	})
	parent.children = slices.Insert(parent.children, pos, child)
	r.index[child.id] = child
	r.observe(child.id)
	return true
}

// flush retries buffered operations until none can make progress.
func (r *RGA) flush() {
	for len(r.pending) > 0 {
		progress := false
		rest := r.pending[:0]
		for _, op := range r.pending {
			if r.integrate(op) {
				progress = true
			} else {
				rest = append(rest, op)
			}
		}
		r.pending = rest
		if !progress {
			return
		}
	}
}

// Entries returns the visible characters with their IDs, in document order.
func (r *RGA) Entries() []Entry {
	var out []Entry
	var walk func(n *node)
	walk = func(n *node) {
		if n != r.root && !n.deleted {
			out = append(out, Entry{ID: n.id, Value: n.value})
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(r.root)
	return out
}

// String renders the visible document.
func (r *RGA) String() string {
	var b strings.Builder
	for _, e := range r.Entries() {
		b.WriteRune(e.Value)
	}
	return b.String()
}

// Len returns the number of visible characters.
func (r *RGA) Len() int { return len(r.Entries()) }

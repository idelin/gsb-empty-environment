package lseq

import (
	"math/rand"
	"strings"
)

// OpType discriminates the two commutative edit operations.
type OpType uint8

const (
	OpInsert OpType = iota
	OpDelete
)

// OpID uniquely identifies an operation: (site, per-site sequence number).
type OpID struct {
	Site uint64
	Seq  uint64
}

// Op is a replicated edit. Inserts carry their allocated identifier, so
// they commute with every other insert; deletes reference the identifier
// tombstoned, so they commute with inserts as well. Any permutation of
// the same op set therefore merges to the same state.
type Op struct {
	ID    OpID
	Type  OpType
	Ident Identifier
	Char  rune
}

type entry struct {
	id      Identifier
	char    rune
	deleted bool // tombstone: keeps merges order-independent
}

// Document is one replica of the LSEQ sequence. Entries stay sorted by
// identifier; visible characters are the non-tombstoned entries in order.
type Document struct {
	site       uint64
	seq        uint64
	alloc      *Allocator
	items      []entry
	seen       map[OpID]struct{}
	deletedIds map[string]struct{} // deletes that won the race against their insert
}

// NewDocument creates a replica for site. rng may be nil.
func NewDocument(site uint64, rng *rand.Rand) *Document {
	return &Document{
		site:       site,
		alloc:      NewAllocator(site, rng),
		seen:       make(map[OpID]struct{}),
		deletedIds: make(map[string]struct{}),
	}
}

// Len returns the number of visible characters.
func (d *Document) Len() int {
	n := 0
	for _, e := range d.items {
		if !e.deleted {
			n++
		}
	}
	return n
}

// visibleIndex maps a visible character index to its slot in d.items.
func (d *Document) visibleIndex(index int) int {
	for i := range d.items {
		if d.items[i].deleted {
			continue
		}
		if index == 0 {
			return i
		}
		index--
	}
	return -1
}

// Insert inserts ch at the visible position index (0..Len) and returns
// the operation to broadcast to other replicas.
func (d *Document) Insert(index int, ch rune) Op {
	var prev, next Identifier
	if index > 0 {
		prev = d.items[d.visibleIndex(index-1)].id
	}
	if index < d.Len() {
		next = d.items[d.visibleIndex(index)].id
	}
	id := d.alloc.Allocate(prev, next)
	op := d.newOp(OpInsert, id, ch)
	d.apply(op)
	return op
}

// Delete removes the visible character at index and returns the operation
// to broadcast.
func (d *Document) Delete(index int) Op {
	slot := d.visibleIndex(index)
	op := d.newOp(OpDelete, d.items[slot].id, 0)
	d.apply(op)
	return op
}

func (d *Document) newOp(t OpType, id Identifier, ch rune) Op {
	d.seq++
	return Op{ID: OpID{Site: d.site, Seq: d.seq}, Type: t, Ident: id, Char: ch}
}

// Merge applies a remote operation. It is idempotent and commutative:
// replicas that merge the same set of ops in any order converge.
func (d *Document) Merge(op Op) bool {
	if _, ok := d.seen[op.ID]; ok {
		return false
	}
	d.apply(op)
	return true
}

func (d *Document) apply(op Op) {
	d.seen[op.ID] = struct{}{}
	pos, found := d.search(op.Ident)
	switch op.Type {
	case OpInsert:
		if found {
			return // duplicate identifier: already present
		}
		_, deleted := d.deletedIds[op.Ident.String()]
		d.items = append(d.items, entry{})
		copy(d.items[pos+1:], d.items[pos:])
		d.items[pos] = entry{id: op.Ident, char: op.Char, deleted: deleted}
	case OpDelete:
		d.deletedIds[op.Ident.String()] = struct{}{}
		if found {
			d.items[pos].deleted = true
		}
	}
}

// search returns the slot where id is or should be inserted.
func (d *Document) search(id Identifier) (int, bool) {
	lo, hi := 0, len(d.items)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch c := Compare(d.items[mid].id, id); {
		case c == 0:
			return mid, true
		case c < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return lo, false
}

// String returns the visible text of the replica.
func (d *Document) String() string {
	var b strings.Builder
	for _, e := range d.items {
		if !e.deleted {
			b.WriteRune(e.char)
		}
	}
	return b.String()
}

// Identifiers returns the identifiers of all entries (tombstones
// included) in order. Every replica converges to the same list.
func (d *Document) Identifiers() []Identifier {
	out := make([]Identifier, len(d.items))
	for i, e := range d.items {
		out[i] = e.id
	}
	return out
}

// MaxDepth returns the deepest identifier currently allocated.
func (d *Document) MaxDepth() int {
	max := 0
	for _, e := range d.items {
		if e.id.Depth() > max {
			max = e.id.Depth()
		}
	}
	return max
}

// DepthHistogram counts identifiers per depth level.
func (d *Document) DepthHistogram() map[int]int {
	h := make(map[int]int)
	for _, e := range d.items {
		h[e.id.Depth()]++
	}
	return h
}

package delta

import (
	"sort"
	"strings"
)

// OpKind discriminates the two kinds of document mutations.
type OpKind uint8

const (
	OpInsert OpKind = iota + 1
	OpDelete
)

// Op is a single immutable mutation, uniquely identified by its Dot.
type Op struct {
	Dot    Dot
	Kind   OpKind
	Time   uint64 // Lamport timestamp, used to order siblings causally
	Parent Dot    // OpInsert: dot of the left neighbor (zero dot = head)
	Value  rune   // OpInsert: the inserted character
	Target Dot    // OpDelete: dot of the insert op being deleted
}

// Delta is a slice of operations not yet acknowledged by a peer, plus
// the sender's causal context. A Delta computed against an empty
// watermark is the full state, so full and incremental sync share one
// representation and one merge path.
type Delta struct {
	Ops []Op
	Ctx VersionVector
}

// Document is a delta-state RGA (Replicated Growable Array) text CRDT.
//
// The state is a dot-keyed operation store plus a causal context; both
// grow monotonically, which makes merges idempotent, commutative and
// associative.
type Document struct {
	replica ReplicaID
	seq     uint64
	clock   uint64 // Lamport clock
	ops     map[Dot]Op
	ctx     VersionVector
}

// NewDocument creates an empty document owned by the given replica.
func NewDocument(replica ReplicaID) *Document {
	return &Document{
		replica: replica,
		ops:     make(map[Dot]Op),
		ctx:     make(VersionVector),
	}
}

// Replica returns the ID of the replica owning this document.
func (d *Document) Replica() ReplicaID { return d.replica }

// Context returns a copy of the causal context (version vector).
func (d *Document) Context() VersionVector { return d.ctx.Clone() }

// OpCount returns the number of operations in the store (including
// tombstones), i.e. the size of a full-state transfer.
func (d *Document) OpCount() int { return len(d.ops) }

func (d *Document) nextDot() Dot {
	d.seq++
	return Dot{Replica: d.replica, Seq: d.seq}
}

// tick advances the Lamport clock past the given observed time and
// returns the new timestamp.
func (d *Document) tick(observed uint64) uint64 {
	if observed > d.clock {
		d.clock = observed
	}
	d.clock++
	return d.clock
}

func (d *Document) store(op Op) {
	d.ops[op.Dot] = op
	if op.Dot.Seq > d.ctx[op.Dot.Replica] {
		d.ctx[op.Dot.Replica] = op.Dot.Seq
	}
	if op.Dot.Replica == d.replica && op.Dot.Seq > d.seq {
		d.seq = op.Dot.Seq
	}
	if op.Time > d.clock {
		d.clock = op.Time
	}
}

// timeOf returns the Lamport time of the op identified by dot
// (0 for the zero dot / unknown ops).
func (d *Document) timeOf(dot Dot) uint64 {
	if op, ok := d.ops[dot]; ok {
		return op.Time
	}
	return 0
}

// InsertAfter inserts value after the element identified by parent
// (zero dot inserts at the head) and returns the new element's dot.
func (d *Document) InsertAfter(parent Dot, value rune) Dot {
	op := Op{
		Dot:    d.nextDot(),
		Kind:   OpInsert,
		Time:   d.tick(d.timeOf(parent)),
		Parent: parent,
		Value:  value,
	}
	d.store(op)
	return op.Dot
}

// Append inserts value at the end of the document.
func (d *Document) Append(value rune) Dot {
	parent := Dot{}
	if vis := d.visible(); len(vis) > 0 {
		parent = vis[len(vis)-1].Dot
	}
	return d.InsertAfter(parent, value)
}

// Delete marks the element identified by target as deleted (tombstone)
// and returns the delete op's dot.
func (d *Document) Delete(target Dot) Dot {
	op := Op{
		Dot:    d.nextDot(),
		Kind:   OpDelete,
		Time:   d.tick(d.timeOf(target)),
		Target: target,
	}
	d.store(op)
	return op.Dot
}

// DotAt returns the dot of the visible element at index.
func (d *Document) DotAt(index int) (Dot, bool) {
	vis := d.visible()
	if index < 0 || index >= len(vis) {
		return Dot{}, false
	}
	return vis[index].Dot, true
}

// Len returns the number of visible (non-deleted) characters.
func (d *Document) Len() int { return len(d.visible()) }

// String materializes the visible document content.
func (d *Document) String() string {
	var sb strings.Builder
	for _, op := range d.visible() {
		sb.WriteRune(op.Value)
	}
	return sb.String()
}

// visible returns the live insert ops in document order. Siblings
// (inserts after the same parent) are ordered by Lamport time
// descending, ties broken by replica ID: a total order that is
// deterministic on every replica and respects causality, so a later
// insert wins over concurrently existing siblings.
func (d *Document) visible() []Op {
	children := make(map[Dot][]Op)
	deleted := make(map[Dot]bool)
	for _, op := range d.ops {
		switch op.Kind {
		case OpInsert:
			children[op.Parent] = append(children[op.Parent], op)
		case OpDelete:
			deleted[op.Target] = true
		}
	}
	for _, list := range children {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.Time != b.Time {
				return a.Time > b.Time
			}
			return a.Dot.Replica > b.Dot.Replica
		})
	}
	var out []Op
	var walk func(parent Dot)
	walk = func(parent Dot) {
		for _, op := range children[parent] {
			if !deleted[op.Dot] {
				out = append(out, op)
			}
			walk(op.Dot) // children of tombstones stay visible
		}
	}
	walk(Dot{})
	return out
}

// DeltaSince returns every stored operation not dominated by watermark,
// together with the current causal context. A nil/empty watermark
// yields the full state.
func (d *Document) DeltaSince(watermark VersionVector) Delta {
	var ops []Op
	for dot, op := range d.ops {
		if !watermark.Dominates(dot) {
			ops = append(ops, op)
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		a, b := ops[i].Dot, ops[j].Dot
		if a.Replica != b.Replica {
			return a.Replica < b.Replica
		}
		return a.Seq < b.Seq
	})
	return Delta{Ops: ops, Ctx: d.Context()}
}

// MergeDelta merges a delta (or a full state) into the document.
// Already-known dots are skipped, so merges are idempotent.
func (d *Document) MergeDelta(delta Delta) {
	for _, op := range delta.Ops {
		if d.ctx.Dominates(op.Dot) {
			continue
		}
		d.store(op)
	}
	d.ctx = d.ctx.Join(delta.Ctx)
}

// EqualState reports whether both documents hold exactly the same
// state: identical causal contexts and identical operation sets.
func (d *Document) EqualState(other *Document) bool {
	if !d.ctx.Equal(other.ctx) {
		return false
	}
	if len(d.ops) != len(other.ops) {
		return false
	}
	for dot, op := range d.ops {
		if other.ops[dot] != op {
			return false
		}
	}
	return true
}

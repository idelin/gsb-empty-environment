// Package seqcrdt implements a sequence CRDT (RGA-style) for collaborative
// text editing. Merged results never depend on message arrival order:
//
//   - Operation IDs are (replica ID, monotonically increasing counter).
//   - Inserts reference the predecessor element ID; deletes reference the
//     target element ID. Array indices are never used as permanent IDs.
//   - Concurrent inserts after the same predecessor are ordered by op ID.
//   - An element's successor subtree is traversed contiguously (DFS).
//   - Deleted elements remain as tombstone anchors but are not visible.
//   - Operations whose dependencies have not arrived are buffered as
//     pending; duplicates are idempotent; same ID with different content
//     is a conflict error.
package seqcrdt

import (
	"errors"
	"fmt"
	"sort"
)

// ID uniquely identifies an operation (and the element it inserts).
type ID struct {
	Replica string `json:"replica"`
	Seq     uint64 `json:"seq"`
}

// RootID is the implicit predecessor of the first element.
var RootID = ID{}

// Less is the deterministic total order on IDs.
func (a ID) Less(b ID) bool {
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	return a.Replica < b.Replica
}

func (id ID) String() string { return fmt.Sprintf("%s:%d", id.Replica, id.Seq) }

// OpKind distinguishes inserts from deletes.
type OpKind uint8

const (
	OpInsert OpKind = iota + 1
	OpDelete
)

// Op is a single CRDT operation. For inserts, Parent is the predecessor
// element ID (RootID for the head) and Value the inserted character.
// For deletes, Target is the element ID to delete.
type Op struct {
	ID     ID     `json:"id"`
	Kind   OpKind `json:"kind"`
	Parent ID     `json:"parent,omitempty"`
	Target ID     `json:"target,omitempty"`
	Value  rune   `json:"value,omitempty"`
}

func (o Op) sameContent(p Op) bool {
	return o.Kind == p.Kind && o.Parent == p.Parent && o.Target == p.Target && o.Value == p.Value
}

// ErrConflict reports the same op ID arriving with different content.
var ErrConflict = errors.New("seqcrdt: conflicting operation content for same ID")

// ErrPosition is returned for out-of-range visible positions.
var ErrPosition = errors.New("seqcrdt: visible position out of range")

// Anchor is the persistent state of one element, including tombstones.
type Anchor struct {
	ID        ID   `json:"id"`
	Parent    ID   `json:"parent"`
	Value     rune `json:"value"`
	Deleted   bool `json:"deleted"`
	DeletedBy ID   `json:"deletedBy,omitempty"`
}

// Doc is one replica of the sequence CRDT.
type Doc struct {
	replica  string
	seq      uint64
	anchors  map[ID]*Anchor
	children map[ID][]ID // children sorted by ID descending
	seen     map[ID]Op   // every op ID ever received (idempotency/conflicts)
	pending  []Op        // ops waiting for missing dependencies
	maxSeq   map[string]uint64
}

// New creates a replica with the given ID.
func New(replica string) *Doc {
	return &Doc{
		replica:  replica,
		anchors:  map[ID]*Anchor{},
		children: map[ID][]ID{},
		seen:     map[ID]Op{},
		maxSeq:   map[string]uint64{},
	}
}

// Replica returns this replica's ID.
func (d *Doc) Replica() string { return d.replica }

// Pending returns a copy of the buffered operations still waiting for
// missing dependencies.
func (d *Doc) Pending() []Op {
	out := make([]Op, len(d.pending))
	copy(out, d.pending)
	return out
}

// Len returns the number of anchors, including tombstones.
func (d *Doc) Len() int { return len(d.anchors) }

func (d *Doc) nextID() ID {
	d.seq++
	return ID{Replica: d.replica, Seq: d.seq}
}

// order returns all element IDs (including tombstones) in deterministic
// traversal order: pre-order DFS where each element's successor subtree
// is contiguous, and siblings (concurrent inserts after the same
// predecessor) are sorted by op ID, newest first.
func (d *Doc) order() []ID {
	out := make([]ID, 0, len(d.anchors))
	var walk func(parent ID)
	walk = func(parent ID) {
		for _, c := range d.children[parent] {
			out = append(out, c)
			walk(c)
		}
	}
	walk(RootID)
	return out
}

// Visible returns the IDs of non-deleted elements in visible order.
func (d *Doc) Visible() []ID {
	var out []ID
	for _, id := range d.order() {
		if !d.anchors[id].Deleted {
			out = append(out, id)
		}
	}
	return out
}

// Text returns the visible sequence as a string.
func (d *Doc) Text() string {
	var b []rune
	for _, id := range d.Visible() {
		b = append(b, d.anchors[id].Value)
	}
	return string(b)
}

// InsertAt generates and locally applies an insert so the new element
// lands at visible position pos (0 <= pos <= visible length). The
// position is only used to pick a predecessor anchor; the emitted op
// references that anchor's ID, never the index.
func (d *Doc) InsertAt(pos int, v rune) (Op, error) {
	full := d.order()
	visibleSeen := 0
	parent := RootID
	for _, id := range full {
		if visibleSeen == pos {
			break
		}
		parent = id // last anchor (visible or tombstone) before position pos
		if !d.anchors[id].Deleted {
			visibleSeen++
		}
	}
	if visibleSeen != pos {
		return Op{}, fmt.Errorf("%w: insert at %d", ErrPosition, pos)
	}
	op := Op{ID: d.nextID(), Kind: OpInsert, Parent: parent, Value: v}
	return op, d.Apply(op)
}

// DeleteAt generates and locally applies a delete of the element at
// visible position pos. The emitted op references the element's ID.
func (d *Doc) DeleteAt(pos int) (Op, error) {
	vis := d.Visible()
	if pos < 0 || pos >= len(vis) {
		return Op{}, fmt.Errorf("%w: delete at %d", ErrPosition, pos)
	}
	op := Op{ID: d.nextID(), Kind: OpDelete, Target: vis[pos]}
	return op, d.Apply(op)
}

// Apply merges one operation, local or remote. Duplicate deliveries are
// idempotent no-ops; the same ID with different content returns
// ErrConflict. Operations with missing dependencies are buffered and
// applied once their dependencies arrive.
func (d *Doc) Apply(op Op) error {
	if prev, ok := d.seen[op.ID]; ok {
		if !prev.sameContent(op) {
			return fmt.Errorf("%w: %s", ErrConflict, op.ID)
		}
		return nil
	}
	d.seen[op.ID] = op
	if op.ID.Seq > d.maxSeq[op.ID.Replica] {
		d.maxSeq[op.ID.Replica] = op.ID.Seq
	}
	if !d.ready(op) {
		d.pending = append(d.pending, op)
		return nil
	}
	d.applyNow(op)
	d.drainPending()
	return nil
}

// ready reports whether all dependencies of op are present.
func (d *Doc) ready(op Op) bool {
	switch op.Kind {
	case OpInsert:
		return op.Parent == RootID || d.anchors[op.Parent] != nil
	case OpDelete:
		return d.anchors[op.Target] != nil
	}
	return false
}

func (d *Doc) applyNow(op Op) {
	switch op.Kind {
	case OpInsert:
		d.anchors[op.ID] = &Anchor{ID: op.ID, Parent: op.Parent, Value: op.Value}
		s := d.children[op.Parent]
		i := sort.Search(len(s), func(i int) bool { return s[i].Less(op.ID) })
		s = append(s, ID{})
		copy(s[i+1:], s[i:])
		s[i] = op.ID
		d.children[op.Parent] = s
	case OpDelete:
		a := d.anchors[op.Target]
		a.Deleted = true
		a.DeletedBy = op.ID
	}
}

// drainPending applies buffered ops whose dependencies have arrived,
// repeating until no further progress (dependencies may be chained).
func (d *Doc) drainPending() {
	for {
		progressed := false
		rest := d.pending[:0]
		for _, op := range d.pending {
			if d.ready(op) {
				d.applyNow(op)
				progressed = true
			} else {
				rest = append(rest, op)
			}
		}
		d.pending = rest
		if !progressed {
			return
		}
	}
}

// Checkpoint captures the full mergeable state: anchors (including
// tombstones), buffered pending ops, and the op log needed to keep
// duplicates idempotent after restore.
type Checkpoint struct {
	Replica string            `json:"replica"`
	Seq     uint64            `json:"seq"`
	Anchors []Anchor          `json:"anchors"`
	Pending []Op              `json:"pending"`
	Seen    []Op              `json:"seen"`
	MaxSeq  map[string]uint64 `json:"maxSeq"`
}

// Snapshot returns a checkpoint of the current state.
func (d *Doc) Snapshot() Checkpoint {
	cp := Checkpoint{
		Replica: d.replica,
		Seq:     d.seq,
		Pending: d.Pending(),
		MaxSeq:  map[string]uint64{},
	}
	for _, a := range d.anchors {
		cp.Anchors = append(cp.Anchors, *a)
	}
	sort.Slice(cp.Anchors, func(i, j int) bool { return cp.Anchors[i].ID.Less(cp.Anchors[j].ID) })
	for _, op := range d.seen {
		cp.Seen = append(cp.Seen, op)
	}
	sort.Slice(cp.Seen, func(i, j int) bool { return cp.Seen[i].ID.Less(cp.Seen[j].ID) })
	for r, n := range d.maxSeq {
		cp.MaxSeq[r] = n
	}
	return cp
}

// Restore rebuilds a replica from a checkpoint; merging can continue
// immediately, including delivery of ops that were pending.
func Restore(cp Checkpoint) *Doc {
	d := New(cp.Replica)
	d.seq = cp.Seq
	for _, a := range cp.Anchors {
		anchor := a
		d.anchors[a.ID] = &anchor
		d.children[a.Parent] = append(d.children[a.Parent], a.ID)
	}
	for p := range d.children {
		s := d.children[p]
		sort.Slice(s, func(i, j int) bool { return s[j].Less(s[i]) })
	}
	for _, op := range cp.Seen {
		d.seen[op.ID] = op
	}
	d.pending = append(d.pending, cp.Pending...)
	for r, n := range cp.MaxSeq {
		d.maxSeq[r] = n
	}
	return d
}

// CollectTombstones removes tombstone anchors, re-parenting their
// children, but only where safe is true for both the tombstone's insert
// ID and the ID of the op that deleted it. safe must return true only
// when every replica has confirmed (acked) that op, so no live
// reference to the anchor can still arrive; tombstones referenced by
// pending ops are never collected. Reclaiming all history is not
// required: callers may keep conservative safe predicates.
func (d *Doc) CollectTombstones(safe func(id ID) bool) int {
	pendingRefs := map[ID]bool{}
	for _, op := range d.pending {
		pendingRefs[op.Parent] = true
		pendingRefs[op.Target] = true
	}
	collected := 0
	for _, id := range d.order() {
		a := d.anchors[id]
		if a == nil || !a.Deleted {
			continue
		}
		if !safe(a.ID) || !safe(a.DeletedBy) {
			continue
		}
		// No pending op may reference the tombstone or any anchor in
		// its subtree: those ops would lose their anchor.
		if d.subtreeReferenced(id, pendingRefs) {
			continue
		}
		// Re-parent children onto the tombstone's parent, replacing the
		// tombstone's own slot and preserving sorted order.
		kids := d.children[id]
		var merged []ID
		for _, s := range d.children[a.Parent] {
			if s != id {
				merged = append(merged, s)
			}
		}
		merged = append(merged, kids...)
		sort.Slice(merged, func(i, j int) bool { return merged[j].Less(merged[i]) })
		d.children[a.Parent] = merged
		delete(d.children, id)
		delete(d.anchors, id)
		collected++
	}
	return collected
}

func (d *Doc) subtreeReferenced(id ID, refs map[ID]bool) bool {
	if refs[id] {
		return true
	}
	for _, c := range d.children[id] {
		if d.subtreeReferenced(c, refs) {
			return true
		}
	}
	return false
}

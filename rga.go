// Package rga implements a sequence CRDT (a Replicated Growable Array)
// for a finite, known set of replicas editing one shared text offline.
//
// Every operation carries a unique ID: the originating replica ID plus a
// per-replica monotonically increasing counter. Inserts reference the ID of
// their predecessor element; deletes reference the ID of their target
// element. Element IDs are permanent logical identities - visible positions
// (array indices) are only used to *generate* operations, never stored.
//
// Convergence does not depend on message arrival order:
//
//   - Concurrent inserts after the same predecessor are ordered by their
//     operation ID (higher counter first, replica ID breaks ties), which is
//     a total order, so every replica sorts siblings identically.
//   - The visible sequence is a depth-first walk: the successor subtree of
//     one insert is always traversed as one contiguous block.
//   - Deleted elements remain as tombstones. They are invisible but stay
//     addressable, so inserts referencing them still have a valid anchor.
//   - Operations whose dependencies (parent or target) have not arrived yet
//     are buffered in a pending set and re-tried whenever a new operation
//     is applied. Duplicates are idempotent; the same ID carrying different
//     content is a conflict and reported as an error.
//
// # Tombstone garbage collection
//
// A tombstone is still referenced as the anchor (parent) of its subtree, so
// it may only be reclaimed when (a) every replica in the finite replica set
// has acknowledged the delete, e.g. its version vector dominates the delete
// op ID, and (b) no pending operation and no live element references the
// tombstone's ID. Reclamation is an optimisation only; it is neither
// required for correctness nor expected to collect all history, so this
// package deliberately does not implement it.
package rga

import (
	"errors"
	"fmt"
	"sort"
)

// ErrConflict is returned when two operations share an ID but differ in
// content. It indicates a buggy or malicious replica, because IDs are
// supposed to be unique per (replica, counter) pair.
var ErrConflict = errors.New("rga: conflicting operation with same ID")

// ID uniquely identifies an operation (and, for inserts, the element it
// creates): the originating replica plus its per-replica counter.
type ID struct {
	Replica string
	Counter uint64
}

func (id ID) String() string { return fmt.Sprintf("%s:%d", id.Replica, id.Counter) }

// rootID is the sentinel predecessor of the first position in the sequence.
var rootID = ID{}

// before reports whether a sorts before b as siblings. The order is total
// and deterministic: higher counter first, then higher replica ID. Every
// replica therefore orders concurrent inserts after the same predecessor
// identically, regardless of arrival order.
func before(a, b ID) bool {
	if a.Counter != b.Counter {
		return a.Counter > b.Counter
	}
	return a.Replica > b.Replica
}

// OpKind discriminates insert and delete operations.
type OpKind uint8

const (
	OpInsert OpKind = iota + 1
	OpDelete
)

// Operation is the unit of replication. It is immutable once created and
// safe to deliver any number of times in any causal order.
type Operation struct {
	Kind OpKind
	// ID of this operation. For inserts it is also the new element's ID.
	ID ID
	// Parent is the predecessor element ID for inserts (zero ID = head).
	Parent ID
	// Target is the element ID a delete refers to.
	Target ID
	// Value is the inserted character.
	Value rune
}

func conflict(op Operation) error {
	return fmt.Errorf("rga: operation %v: %w", op.ID, ErrConflict)
}

// element is one node of the sequence tree. Deleted elements are kept as
// anchors so that inserts referencing them remain positionable.
type element struct {
	id      ID
	parent  ID
	value   rune
	deleted bool
}

// VisibleElement is one visible (non-deleted) character plus its permanent
// ID, so callers can generate follow-up operations without using indices.
type VisibleElement struct {
	ID    ID
	Value rune
}

// RGA is the replicated sequence state of one replica.
type RGA struct {
	replica  string
	counter  uint64
	elems    map[ID]*element
	children map[ID][]ID // parent -> children, sorted by `before`
	dels     map[ID]ID   // delete op ID -> target, for idempotence/conflicts
	pending  []Operation // ops waiting for missing dependencies
}

// New creates the state for one replica. replica must be non-empty and
// unique within the finite replica set.
func New(replica string) *RGA {
	if replica == "" {
		panic("rga: replica ID must be non-empty")
	}
	return &RGA{
		replica:  replica,
		elems:    make(map[ID]*element),
		children: make(map[ID][]ID),
		dels:     make(map[ID]ID),
	}
}

func (r *RGA) nextID() ID {
	r.counter++
	return ID{Replica: r.replica, Counter: r.counter}
}

// addChild inserts id into the sorted child list of parent.
func (r *RGA) addChild(parent, id ID) {
	kids := r.children[parent]
	i := sort.Search(len(kids), func(i int) bool { return before(id, kids[i]) })
	kids = append(kids, ID{})
	copy(kids[i+1:], kids[i:])
	kids[i] = id
	r.children[parent] = kids
}

// Apply integrates one operation, locally generated or received. It returns
// ErrConflict if an operation with the same ID but different content was
// seen before. Missing dependencies are not an error: the operation is
// buffered and applied once they arrive.
func (r *RGA) Apply(op Operation) error {
	applied, err := r.tryApply(op)
	if err != nil {
		return err
	}
	if !applied {
		return r.addPending(op)
	}
	return r.drainPending()
}

// tryApply applies op if all its dependencies are present. It reports
// applied=false when the operation must wait in the pending set.
func (r *RGA) tryApply(op Operation) (applied bool, err error) {
	switch op.Kind {
	case OpInsert:
		if el, ok := r.elems[op.ID]; ok {
			if el.parent != op.Parent || el.value != op.Value {
				return false, conflict(op)
			}
			return true, nil // duplicate delivery: idempotent
		}
		if op.Parent != rootID {
			if _, ok := r.elems[op.Parent]; !ok {
				return false, nil // predecessor not here yet
			}
		}
		r.elems[op.ID] = &element{id: op.ID, parent: op.Parent, value: op.Value}
		r.addChild(op.Parent, op.ID)
		return true, nil
	case OpDelete:
		if target, ok := r.dels[op.ID]; ok {
			if target != op.Target {
				return false, conflict(op)
			}
			return true, nil // duplicate delivery: idempotent
		}
		el, ok := r.elems[op.Target]
		if !ok {
			return false, nil // target not here yet
		}
		el.deleted = true
		r.dels[op.ID] = op.Target
		return true, nil
	default:
		return false, fmt.Errorf("rga: unknown operation kind %d", op.Kind)
	}
}

// addPending buffers op, dropping exact duplicates and rejecting conflicts
// against already buffered operations.
func (r *RGA) addPending(op Operation) error {
	for _, p := range r.pending {
		if p.ID == op.ID {
			if p != op {
				return conflict(op)
			}
			return nil
		}
	}
	r.pending = append(r.pending, op)
	return nil
}

// drainPending re-tries buffered operations until no more can be applied.
func (r *RGA) drainPending() error {
	for len(r.pending) > 0 {
		progressed := false
		remaining := r.pending[:0]
		for _, op := range r.pending {
			applied, err := r.tryApply(op)
			if err != nil {
				return err
			}
			if applied {
				progressed = true
			} else {
				remaining = append(remaining, op)
			}
		}
		r.pending = remaining
		if !progressed {
			return nil
		}
	}
	return nil
}

// Pending returns a copy of the operations still waiting for dependencies.
func (r *RGA) Pending() []Operation {
	return append([]Operation(nil), r.pending...)
}

// Visible returns the visible sequence in deterministic order: a
// depth-first walk where each element's successor subtree is one contiguous
// block and tombstones are skipped but still traversed through.
func (r *RGA) Visible() []VisibleElement {
	var out []VisibleElement
	var walk func(parent ID)
	walk = func(parent ID) {
		for _, id := range r.children[parent] {
			el := r.elems[id]
			if !el.deleted {
				out = append(out, VisibleElement{ID: id, Value: el.value})
			}
			walk(id)
		}
	}
	walk(rootID)
	return out
}

// String returns the visible text.
func (r *RGA) String() string {
	vis := r.Visible()
	buf := make([]rune, len(vis))
	for i, ve := range vis {
		buf[i] = ve.Value
	}
	return string(buf)
}

// InsertAt generates and applies an insert at visible position pos
// (0 = before the first visible element) and returns the operation for
// broadcast. The operation references the permanent ID of the predecessor
// element, not the position.
func (r *RGA) InsertAt(pos int, v rune) (Operation, error) {
	vis := r.Visible()
	if pos < 0 || pos > len(vis) {
		return Operation{}, fmt.Errorf("rga: insert position %d out of range [0,%d]", pos, len(vis))
	}
	parent := rootID
	if pos > 0 {
		parent = vis[pos-1].ID
	}
	op := Operation{Kind: OpInsert, ID: r.nextID(), Parent: parent, Value: v}
	if err := r.Apply(op); err != nil {
		return Operation{}, err
	}
	return op, nil
}

// DeleteAt generates and applies a delete of the visible element at pos and
// returns the operation for broadcast. The operation references the
// permanent ID of the target element, not the position.
func (r *RGA) DeleteAt(pos int) (Operation, error) {
	vis := r.Visible()
	if pos < 0 || pos >= len(vis) {
		return Operation{}, fmt.Errorf("rga: delete position %d out of range [0,%d)", pos, len(vis))
	}
	op := Operation{Kind: OpDelete, ID: r.nextID(), Target: vis[pos].ID}
	if err := r.Apply(op); err != nil {
		return Operation{}, err
	}
	return op, nil
}

// ElementRecord is the serialisable form of one element, tombstones
// included, because they are still needed as anchors.
type ElementRecord struct {
	ID      ID
	Parent  ID
	Value   rune
	Deleted bool
}

// DeleteRecord records one applied delete for idempotence and conflict
// detection after restore.
type DeleteRecord struct {
	Op     ID
	Target ID
}

// Checkpoint is the full serialisable state: every element (including
// tombstone anchors), applied deletes, the local counter, and all pending
// operations. It contains only value types and slices, so it can be encoded
// with encoding/json, encoding/gob, etc.
type Checkpoint struct {
	Replica  string
	Counter  uint64
	Elements []ElementRecord
	Deletes  []DeleteRecord
	Pending  []Operation
}

// Checkpoint captures the current state, including anchors and pending
// operations, in a deterministic order.
func (r *RGA) Checkpoint() Checkpoint {
	cp := Checkpoint{
		Replica:  r.replica,
		Counter:  r.counter,
		Elements: make([]ElementRecord, 0, len(r.elems)),
		Deletes:  make([]DeleteRecord, 0, len(r.dels)),
		Pending:  r.Pending(),
	}
	for _, el := range r.elems {
		cp.Elements = append(cp.Elements, ElementRecord{
			ID:      el.id,
			Parent:  el.parent,
			Value:   el.value,
			Deleted: el.deleted,
		})
	}
	sort.Slice(cp.Elements, func(i, j int) bool {
		return before(cp.Elements[i].ID, cp.Elements[j].ID)
	})
	for op, target := range r.dels {
		cp.Deletes = append(cp.Deletes, DeleteRecord{Op: op, Target: target})
	}
	sort.Slice(cp.Deletes, func(i, j int) bool {
		return before(cp.Deletes[i].Op, cp.Deletes[j].Op)
	})
	return cp
}

// Restore rebuilds a replica from a checkpoint. Merging can continue
// immediately; pending operations are retried as their dependencies arrive.
func Restore(cp Checkpoint) (*RGA, error) {
	r := New(cp.Replica)
	r.counter = cp.Counter
	for _, rec := range cp.Elements {
		if _, dup := r.elems[rec.ID]; dup {
			return nil, fmt.Errorf("rga: duplicate element %v in checkpoint", rec.ID)
		}
		r.elems[rec.ID] = &element{id: rec.ID, parent: rec.Parent, value: rec.Value, deleted: rec.Deleted}
	}
	for _, rec := range cp.Elements {
		if rec.Parent != rootID {
			if _, ok := r.elems[rec.Parent]; !ok {
				return nil, fmt.Errorf("rga: element %v in checkpoint has missing parent %v", rec.ID, rec.Parent)
			}
		}
		r.addChild(rec.Parent, rec.ID)
	}
	for _, d := range cp.Deletes {
		r.dels[d.Op] = d.Target
	}
	r.pending = append(r.pending, cp.Pending...)
	return r, nil
}

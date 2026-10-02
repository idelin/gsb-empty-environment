// Package crdt implements a composable JSON CRDT: arbitrarily nested
// Map / Array / Register nodes that merge precisely under concurrent edits.
//
// The design is op-based (CmRDT). Every mutation emits an Op into a causal
// log; replicas converge by exchanging and replaying ops. All ops are
// idempotent and commutative, so Merge can be called in any order, any
// number of times, and every replica converges to the same state.
//
// Conflict resolution rules:
//   - Map:      LWW per key, keyed by (lamport, replica).
//   - Array:    RGA — inserts are anchored after an element id; concurrent
//     inserts after the same anchor order by (lamport, id).
//   - Register: LWW by (lamport, replica).
//
// Composite nodes have stable identity: a Map/Array node is created once
// and mutated in place thereafter, so concurrent edits to different fields
// of the same object never overwrite the object itself.
package crdt

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is the JSON node type of a CRDT node.
type Kind int

const (
	KindMap Kind = iota
	KindArray
	KindRegister
)

func (k Kind) String() string {
	switch k {
	case KindMap:
		return "map"
	case KindArray:
		return "array"
	case KindRegister:
		return "register"
	}
	return "unknown"
}

// NodeID uniquely identifies a CRDT node across all replicas.
// Format: "<replica>:<seq>". The root node uses the well-known id "root".
type NodeID string

// RootID is the well-known id of the document root (always a Map).
const RootID = NodeID("root")

func replicaOf(id NodeID) string {
	s := string(id)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i]
	}
	return s
}

// lessClock orders (lamport, replica) pairs for deterministic LWW.
func lessClock(l1 uint64, r1 string, l2 uint64, r2 string) bool {
	if l1 != l2 {
		return l1 < l2
	}
	return r1 < r2
}

// --- internal node state ---

type mapEntry struct {
	child   NodeID
	lamport uint64
	replica string
}

type arrayElem struct {
	id      NodeID // element id == id of the insert op
	child   NodeID
	lamport uint64
}

type registerValue struct {
	value   any
	lamport uint64
	replica string
}

type node struct {
	id      NodeID
	kind    Kind
	entries map[string]*mapEntry // KindMap
	elems   []*arrayElem         // KindArray
	reg     registerValue        // KindRegister
}

// --- operations ---

// OpKind discriminates the mutation carried by an Op.
type OpKind int

const (
	OpMapSet OpKind = iota
	OpArrayInsert
	OpRegisterSet
)

// Op is a single replicated mutation. Ops are self-contained: the attach
// ops (MapSet / ArrayInsert) also create their child node on first apply,
// so a replica only needs the op stream to reconstruct full state.
type Op struct {
	ID        NodeID // unique op id, assigned by the emitting replica
	Lamport   uint64
	Kind      OpKind
	Parent    NodeID // node this op targets
	Key       string // OpMapSet: map key
	After     NodeID // OpArrayInsert: anchor element id ("" = head)
	Child     NodeID // node id created/referenced by attach ops
	ChildKind Kind   // kind of the child node created by attach ops
	Value     any    // register payload (creation value or OpRegisterSet)
}

// --- document ---

// Doc is one replica of the collaborative JSON document.
type Doc struct {
	replica string
	seq     uint64
	lamport uint64
	nodes   map[NodeID]*node
	seen    map[NodeID]bool
	pending []Op // ops waiting on missing causal dependencies
	log     []Op // all applied ops, local and merged (for transitive sync)
}

// New creates a replica. replica must be unique across all peers.
func New(replica string) *Doc {
	d := &Doc{
		replica: replica,
		nodes:   map[NodeID]*node{},
		seen:    map[NodeID]bool{},
	}
	d.nodes[RootID] = &node{id: RootID, kind: KindMap, entries: map[string]*mapEntry{}}
	return d
}

// Root returns a handle to the root Map node.
func (d *Doc) Root() Ref { return Ref{doc: d, id: RootID} }

// Ops returns a copy of the replica's applied op log, suitable for
// shipping to peers over any transport.
func (d *Doc) Ops() []Op {
	out := make([]Op, len(d.log))
	copy(out, d.log)
	return out
}

// Apply integrates a batch of remote ops (e.g. received over the wire).
func (d *Doc) Apply(ops []Op) {
	for _, op := range ops {
		d.apply(op)
	}
}

// Merge integrates every op known to other. Convergent, commutative,
// idempotent: after pairwise merges in any order, all replicas agree.
func (d *Doc) Merge(other *Doc) {
	d.Apply(other.log)
}

func (d *Doc) newNodeID() NodeID {
	d.seq++
	return NodeID(fmt.Sprintf("%s:%d", d.replica, d.seq))
}

// emit assigns id/clock to a locally generated op and applies it.
// Returns the op id (used as the element id for array inserts).
func (d *Doc) emit(op Op) NodeID {
	op.ID = d.newNodeID()
	d.lamport++
	op.Lamport = d.lamport
	d.apply(op)
	return op.ID
}

func (d *Doc) apply(op Op) {
	d.pending = append(d.pending, op)
	d.flush()
}

// flush applies every pending op whose causal dependencies are satisfied,
// repeating until no further progress can be made.
func (d *Doc) flush() {
	for {
		progressed := false
		remaining := d.pending[:0]
		for _, op := range d.pending {
			switch {
			case d.seen[op.ID]:
				// duplicate delivery, drop
			case !d.depsReady(op):
				remaining = append(remaining, op)
			default:
				d.seen[op.ID] = true
				if op.Lamport > d.lamport {
					d.lamport = op.Lamport
				}
				d.mutate(op)
				d.log = append(d.log, op)
				progressed = true
			}
		}
		d.pending = remaining
		if !progressed {
			return
		}
	}
}

func (d *Doc) depsReady(op Op) bool {
	parent, ok := d.nodes[op.Parent]
	if !ok {
		return false
	}
	if op.Kind == OpArrayInsert && op.After != "" {
		for _, e := range parent.elems {
			if e.id == op.After {
				return true
			}
		}
		return false
	}
	return true
}

func (d *Doc) mutate(op Op) {
	switch op.Kind {
	case OpMapSet:
		d.ensureNode(op.Child, op.ChildKind, op.Value, op.Lamport, replicaOf(op.ID))
		parent := d.nodes[op.Parent]
		replica := replicaOf(op.ID)
		if e, ok := parent.entries[op.Key]; ok {
			if !lessClock(e.lamport, e.replica, op.Lamport, replica) {
				return // existing entry wins the LWW compare
			}
		}
		parent.entries[op.Key] = &mapEntry{child: op.Child, lamport: op.Lamport, replica: replica}

	case OpArrayInsert:
		d.ensureNode(op.Child, op.ChildKind, op.Value, op.Lamport, replicaOf(op.ID))
		parent := d.nodes[op.Parent]
		for _, e := range parent.elems {
			if e.id == op.ID {
				return // element already inserted
			}
		}
		anchor := -1
		if op.After != "" {
			for i, e := range parent.elems {
				if e.id == op.After {
					anchor = i
					break
				}
			}
		}
		// RGA: skip past elements inserted concurrently with a higher
		// (lamport, id) so every replica computes the same position.
		at := anchor + 1
		for at < len(parent.elems) {
			e := parent.elems[at]
			if e.lamport > op.Lamport || (e.lamport == op.Lamport && e.id > op.ID) {
				at++
			} else {
				break
			}
		}
		parent.elems = append(parent.elems, nil)
		copy(parent.elems[at+1:], parent.elems[at:])
		parent.elems[at] = &arrayElem{id: op.ID, child: op.Child, lamport: op.Lamport}

	case OpRegisterSet:
		n := d.nodes[op.Parent]
		replica := replicaOf(op.ID)
		if lessClock(n.reg.lamport, n.reg.replica, op.Lamport, replica) {
			n.reg = registerValue{value: op.Value, lamport: op.Lamport, replica: replica}
		}
	}
}

// ensureNode creates a node if absent; node creation is idempotent so
// redelivery of the creating attach op is a no-op.
func (d *Doc) ensureNode(id NodeID, kind Kind, value any, lamport uint64, replica string) {
	if _, ok := d.nodes[id]; ok {
		return
	}
	n := &node{id: id, kind: kind}
	switch kind {
	case KindMap:
		n.entries = map[string]*mapEntry{}
	case KindRegister:
		n.reg = registerValue{value: value, lamport: lamport, replica: replica}
	}
	d.nodes[id] = n
}

// emitAttach emits the op that attaches v under parent (by key for maps,
// after `after` for arrays), recursively emitting ops for nested
// map[string]any / []any subtrees. Returns the attach op id and the id of
// the created child node.
func (d *Doc) emitAttach(parent NodeID, key string, after NodeID, v any) (opID NodeID, childID NodeID) {
	kind := OpMapSet
	if d.nodes[parent].kind == KindArray {
		kind = OpArrayInsert
	}
	switch val := v.(type) {
	case map[string]any:
		child := d.newNodeID()
		opID = d.emit(Op{Kind: kind, Parent: parent, Key: key, After: after, Child: child, ChildKind: KindMap})
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			d.emitAttach(child, k, "", val[k])
		}
		return opID, child
	case []any:
		child := d.newNodeID()
		opID = d.emit(Op{Kind: kind, Parent: parent, Key: key, After: after, Child: child, ChildKind: KindArray})
		last := NodeID("")
		for _, item := range val {
			last, _ = d.emitAttach(child, "", last, item)
		}
		return opID, child
	default: // primitive -> register
		child := d.newNodeID()
		opID = d.emit(Op{Kind: kind, Parent: parent, Key: key, After: after, Child: child, ChildKind: KindRegister, Value: val})
		return opID, child
	}
}

// ToJSON materializes the document as plain Go JSON values
// (map[string]any / []any / primitives).
func (d *Doc) ToJSON() any { return d.nodeToJSON(RootID) }

func (d *Doc) nodeToJSON(id NodeID) any {
	n := d.nodes[id]
	switch n.kind {
	case KindMap:
		m := make(map[string]any, len(n.entries))
		for k, e := range n.entries {
			m[k] = d.nodeToJSON(e.child)
		}
		return m
	case KindArray:
		arr := make([]any, 0, len(n.elems))
		for _, e := range n.elems {
			arr = append(arr, d.nodeToJSON(e.child))
		}
		return arr
	case KindRegister:
		return n.reg.value
	}
	return nil
}

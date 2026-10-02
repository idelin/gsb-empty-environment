package crdt

import (
	"encoding/json"
	"fmt"
)

// Ref is a stable handle to a node in the document. Because nodes have
// identity independent of their position, a Ref captured before a merge
// still points at the same in-memory node afterwards — concurrent edits
// merge into the node in place rather than replacing it.
type Ref struct {
	doc *Doc
	id  NodeID
}

// ID returns the stable, globally unique id of the referenced node.
func (r Ref) ID() NodeID { return r.id }

// Kind reports the node type of the referenced node.
func (r Ref) Kind() Kind { return r.doc.nodes[r.id].kind }

func (r Ref) mustKind(k Kind) {
	if got := r.Kind(); got != k {
		panic(fmt.Sprintf("crdt: node %s is %s, not %s", r.id, got, k))
	}
}

// --- Map operations ---

// Set assigns key to v on a Map node. v may be a JSON primitive
// (string/float64/int/bool/nil), map[string]any, []any, or any nesting
// thereof; composite values are materialized as nested CRDT nodes.
func (r Ref) Set(key string, v any) {
	r.mustKind(KindMap)
	r.doc.emitAttach(r.id, key, "", v)
}

// Get returns the child node stored at key on a Map node.
func (r Ref) Get(key string) (Ref, bool) {
	r.mustKind(KindMap)
	e, ok := r.doc.nodes[r.id].entries[key]
	if !ok {
		return Ref{}, false
	}
	return Ref{doc: r.doc, id: e.child}, true
}

// Map returns the Map node at key, creating and attaching one if absent.
func (r Ref) Map(key string) Ref {
	r.mustKind(KindMap)
	if child, ok := r.Get(key); ok && child.Kind() == KindMap {
		return child
	}
	r.Set(key, map[string]any{})
	child, _ := r.Get(key)
	return child
}

// Array returns the Array node at key, creating and attaching one if absent.
func (r Ref) Array(key string) Ref {
	r.mustKind(KindMap)
	if child, ok := r.Get(key); ok && child.Kind() == KindArray {
		return child
	}
	r.Set(key, []any{})
	child, _ := r.Get(key)
	return child
}

// --- Array operations ---

// Push appends v to an Array node and returns a handle to the new element.
// v accepts the same shapes as Set.
func (r Ref) Push(v any) Ref {
	r.mustKind(KindArray)
	n := r.doc.nodes[r.id]
	var after NodeID
	if len(n.elems) > 0 {
		after = n.elems[len(n.elems)-1].id
	}
	_, child := r.doc.emitAttach(r.id, "", after, v)
	return Ref{doc: r.doc, id: child}
}

// PushMap appends an empty Map element and returns its handle.
func (r Ref) PushMap() Ref { return r.Push(map[string]any{}) }

// PushArray appends an empty Array element and returns its handle.
func (r Ref) PushArray() Ref { return r.Push([]any{}) }

// Len returns the number of elements in an Array node.
func (r Ref) Len() int {
	r.mustKind(KindArray)
	return len(r.doc.nodes[r.id].elems)
}

// Index returns a handle to the i-th element of an Array node.
func (r Ref) Index(i int) Ref {
	r.mustKind(KindArray)
	n := r.doc.nodes[r.id]
	if i < 0 || i >= len(n.elems) {
		panic(fmt.Sprintf("crdt: index %d out of range (len %d)", i, len(n.elems)))
	}
	return Ref{doc: r.doc, id: n.elems[i].child}
}

// --- Register operations ---

// SetValue overwrites a Register node (last-writer-wins).
func (r Ref) SetValue(v any) {
	r.mustKind(KindRegister)
	r.doc.emit(Op{Kind: OpRegisterSet, Parent: r.id, Value: v})
}

// Value reads the current value of a Register node.
func (r Ref) Value() any {
	r.mustKind(KindRegister)
	return r.doc.nodes[r.id].reg.value
}

// --- materialization ---

// ToJSON materializes the subtree rooted at this node as plain Go JSON values.
func (r Ref) ToJSON() any { return r.doc.nodeToJSON(r.id) }

// String returns the canonical JSON encoding of the subtree.
func (r Ref) String() string {
	b, err := json.Marshal(r.ToJSON())
	if err != nil {
		panic(err)
	}
	return string(b)
}

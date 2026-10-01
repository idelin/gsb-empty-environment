// Package canon implements a canonical binary serialization for a
// self-defined data model, suitable as a stable input to digest/signature
// schemes (it does not implement any signature algorithm itself).
//
// Supported value model (the serialization scope):
//
//	Null, Bool, Int (int64), Float (float64, finite only; NaN and
//	+/-Inf are explicitly rejected), String (valid UTF-8 only),
//	Array (order-preserving), Object (string keys, encoded sorted by
//	key bytes; duplicate keys rejected), and Graph (a rooted, labeled,
//	directed graph with ordered adjacency, supporting shared references
//	and cycles).
//
// Canonicity guarantees:
//   - Object key insertion order does not affect the encoding.
//   - Distinct types have distinct encodings: Int(1) != String("1"),
//     Int(1) != Float(1).
//   - A graph's encoding depends only on its rooted labeled graph
//     isomorphism class (labels + ordered adjacency + root), never on
//     node allocation order, traversal/discovery order, or pointer
//     identity.
//
// Decoding never executes host code; it only constructs data. All
// length prefixes are validated against the remaining input before any
// allocation proportional to them is made.
package canon

// Kind identifies the type of a Value.
type Kind int

const (
	KindNull Kind = iota
	KindBool
	KindInt
	KindFloat
	KindString
	KindArray
	KindObject
	KindGraph
)

// Pair is one key/value entry of an object.
type Pair struct {
	Key   string
	Value Value
}

// Value is a node in the supported data model. The zero value is Null.
type Value struct {
	kind Kind
	bval bool
	ival int64
	fval float64
	sval string
	aval []Value
	oval []Pair
	gval *Graph
}

// Kind returns the kind of v.
func (v Value) Kind() Kind { return v.kind }

// Null returns the null value.
func Null() Value { return Value{kind: KindNull} }

// Bool returns a boolean value.
func Bool(b bool) Value { return Value{kind: KindBool, bval: b} }

// Int returns an integer value.
func Int(i int64) Value { return Value{kind: KindInt, ival: i} }

// Float returns a floating-point value. Non-finite values (NaN, +/-Inf)
// are rejected at encoding time.
func Float(f float64) Value { return Value{kind: KindFloat, fval: f} }

// String returns a string value. s must be valid UTF-8 to be encodable.
func String(s string) Value { return Value{kind: KindString, sval: s} }

// Array returns an array value. Element order is significant and preserved.
func Array(elems ...Value) Value { return Value{kind: KindArray, aval: elems} }

// Object returns an object value. Key order in pairs is irrelevant;
// keys are sorted canonically at encoding time. Duplicate keys are
// rejected at encoding time.
func Object(pairs ...Pair) Value { return Value{kind: KindObject, oval: pairs} }

// ObjectFromMap returns an object value built from a map.
func ObjectFromMap(m map[string]Value) Value {
	pairs := make([]Pair, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, Pair{Key: k, Value: v})
	}
	return Value{kind: KindObject, oval: pairs}
}

// GraphValue wraps a rooted graph as a value. Only nodes reachable from
// root are part of the encoded graph.
func GraphValue(root *Node) Value { return Value{kind: KindGraph, gval: &Graph{Root: root}} }

// Graph is a rooted directed graph. Edges are ordered: the order of a
// node's Edges slice is significant, like an array.
type Graph struct {
	Root *Node
}

// Node is a graph vertex with a label and an ordered list of outgoing
// edges. The label may be any non-graph value. Nodes may be shared by
// multiple parents and may form cycles.
type Node struct {
	Label Value
	Edges []*Node
}

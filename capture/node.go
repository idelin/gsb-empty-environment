package capture

// Kind identifies what a Node holds.
type Kind int

const (
	KindUint   Kind = iota // Uint64 scalar
	KindBytes              // CString or Blob
	KindPtr                // pointer slot; Elem is the target or nil
	KindStruct             // fixed-size record
	KindSlice              // Counted header plus elements
)

// Node is one captured value. The graph is built once by Capture and
// never mutated afterwards; accessors that expose slices return copies,
// so a snapshot is safe to hold and share without synchronization.
//
// Nodes may form cycles (a list node pointing at itself): compare with
// Node pointers, do not walk recursively without a visited set.
type Node struct {
	kind   Kind
	addr   uint64
	u64    uint64
	bytes  []byte
	elem   *Node
	fields []fieldNode
	elems  []*Node
}

type fieldNode struct {
	name  string
	child *Node
}

// Kind reports what the node holds.
func (n *Node) Kind() Kind { return n.kind }

// Addr is the address the node was captured from. Nodes shared through
// repeated pointers have the same Addr and are the same *Node.
func (n *Node) Addr() uint64 { return n.addr }

// Uint returns the scalar of a KindUint node.
func (n *Node) Uint() uint64 { return n.u64 }

// Bytes returns a copy of the bytes of a KindBytes node.
func (n *Node) Bytes() []byte {
	return append([]byte(nil), n.bytes...)
}

// Elem returns the target of a KindPtr node, or nil for a null pointer.
func (n *Node) Elem() *Node { return n.elem }

// Field returns the child captured for the named struct field, or nil
// if the schema had no such field.
func (n *Node) Field(name string) *Node {
	for _, f := range n.fields {
		if f.name == name {
			return f.child
		}
	}
	return nil
}

// Len returns the element count of a KindSlice node.
func (n *Node) Len() int { return len(n.elems) }

// Elems returns a copy of the element list of a KindSlice node.
func (n *Node) Elems() []*Node {
	return append([]*Node(nil), n.elems...)
}

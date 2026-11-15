package budget

import (
	"context"
	"io"
)

// DecodeFunc streams the decoded bytes of one leaf into w. Implementations
// must only emit through w: there is no other way to publish bytes, and every
// Write is gated by the budget. The context is cancelled as soon as any limit
// is violated anywhere in the tree, so long-running decodes should select on
// ctx.Done. Returning a non-nil error aborts the whole unfold.
type DecodeFunc func(ctx context.Context, w io.Writer) error

// Node is one vertex of the caller-supplied container tree.
//
// A node with a non-nil Decode is a leaf byte stream. A node with a nil Decode
// is a container whose Children are unfolded. A node carrying both Decode and
// Children is rejected as invalid rather than silently dropping one side.
//
// Nodes may be shared between parents (a DAG): every reference is visited and
// charged independently. Cycles (a node reachable from itself) are rejected.
//
// DeclaredSize is advisory metadata from the untrusted input and is never used
// in an enforcement decision; only bytes actually written count.
type Node struct {
	Name         string
	DeclaredSize int64
	Decode       DecodeFunc
	Children     []*Node
}

func (n *Node) isLeaf() bool {
	return n != nil && n.Decode != nil
}

// Leaf is one successfully decoded leaf reference in publication order.
type Leaf struct {
	// Name is the leaf node's Name.
	Name string

	// Path holds the node names from the root to this leaf, inclusive. It is
	// a per-leaf copy and safe to retain after the unfold.
	Path []string

	// Data is the fully staged decoded payload. Ownership is transferred to
	// the caller when Result is published.
	Data []byte
}

// Result is published only when the entire tree unfolded within budget.
type Result struct {
	// Leaves are the decoded leaves in depth-first reference order.
	Leaves []Leaf

	// TotalBytes is the sum of bytes actually decoded across every reference.
	TotalBytes int64

	// PeakLiveBuffers is the measured maximum number of buffers in flight at
	// once. Always <= Limits.MaxLiveBuffers.
	PeakLiveBuffers int

	// PeakBufferedBytes is the measured maximum number of staged bytes held at
	// any instant. Always <= Limits.MaxTotalBytes.
	PeakBufferedBytes int64
}

package netsec

import (
	"context"
	"io"
)

// DecodeFunc emits the decoded contents of a stream node. The function may
// only write through the supplied io.Writer. The writer enforces all budgets
// atomically before accepting bytes, and returns an error once the expansion
// has been canceled. DecodeFunc implementations should honor both ctx and
// writer errors.
type DecodeFunc func(ctx context.Context, out io.Writer) error

// Node is one node in a caller-provided container tree.
//
// A node is either a stream node (Decode is non-nil) or a container node
// (Decode is nil and Children contains its children). A container with no
// children is an empty terminal node. A node must not be both a stream and a
// container.
//
// DeclaredSize is advisory metadata from the caller. It is deliberately never
// used for budgeting, preallocation, or validation: actual bytes accepted by
// the controlled writer are the only source of truth.
//
// The same *Node may appear more than once in the tree. Every reference is
// planned and decoded independently, so shared references each consume budget
// and each produce their own result. Cycles are rejected before any decoder is
// invoked. The tree must not be mutated while Expand is running.
type Node struct {
	Name         string
	DeclaredSize int64
	Children     []*Node
	Decode       DecodeFunc
}

// Stream returns a stream node with the supplied declared size. The declared
// size is advisory only and is never trusted.
func Stream(name string, declaredSize int64, decode DecodeFunc) *Node {
	return &Node{Name: name, DeclaredSize: declaredSize, Decode: decode}
}

// Container returns a container node with the supplied children.
func Container(name string, children ...*Node) *Node {
	return &Node{Name: name, Children: children}
}

// Kind identifies whether an expanded node held a byte stream or children.
type Kind uint8

const (
	// KindContainer is an expanded container node.
	KindContainer Kind = iota
	// KindStream is an expanded stream node.
	KindStream
)

// ResultNode is one node in the fully expanded result tree. The result is only
// returned after the entire input tree has decoded successfully.
type ResultNode struct {
	Name     string
	Kind     Kind
	Data     []byte
	Children []*ResultNode
}

// Result is a complete, atomically published expansion.
type Result struct {
	Roots []*ResultNode
	Stats Stats
}

// Stats describes the work performed for the most recent expansion attempt.
type Stats struct {
	// DecodedBytes is the number of bytes accepted by controlled writers.
	DecodedBytes int64
	// Leaves is the number of planned terminal-node references. Shared nodes
	// count once per reference.
	Leaves int64
	// MaxDepth is the deepest root-to-leaf path in nodes (a root has depth 1).
	MaxDepth int
	// PeakLiveBytes is the high-water mark of bytes held in live staging
	// buffers. It never exceeds Budget.MaxLiveBuffer.
	PeakLiveBytes int64
}

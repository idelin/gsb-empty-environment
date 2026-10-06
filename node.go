package bnb

import (
	"fmt"
	"strings"
)

// Node is a frontier node of the branch-and-bound tree. It represents the
// partial assignment x_0..x_{d-1} recorded in path; variables from depth d
// onward are still free.
type Node struct {
	path   []int64 // path[k] = chosen value of variable k
	weight int64   // total weight of the fixed prefix
	value  int64   // total value of the fixed prefix
	bound  int64   // relaxation upper bound on any completion of this node
}

// Depth returns the number of fixed variables.
func (n *Node) Depth() int { return len(n.path) }

// Bound returns the node's relaxation upper bound.
func (n *Node) Bound() int64 { return n.bound }

// Path returns a copy of the node's path (fixed variable assignments).
func (n *Node) Path() []int64 {
	out := make([]int64, len(n.path))
	copy(out, n.path)
	return out
}

// PathKey returns the canonical path encoding "v0.v1.v2..." used for
// tie-breaking and checkpoint serialization.
func (n *Node) PathKey() string { return PathKey(n.path) }

// PathKey encodes a path as "v0.v1.v2...".
func PathKey(path []int64) string {
	var sb strings.Builder
	for i, v := range path {
		if i > 0 {
			sb.WriteByte('.')
		}
		fmt.Fprintf(&sb, "%d", v)
	}
	return sb.String()
}

// lessPath reports whether path a sorts before path b. The order is
// element-wise lexicographic with shorter prefixes first; it is equivalent
// to the lexicographic order of any fixed-width path encoding. Because
// every node in the frontier has a distinct path, the priority order
// (bound desc, then path asc) is total, which makes the search order fully
// deterministic and checkpoints exactly reproducible.
func lessPath(a, b []int64) bool {
	k := len(a)
	if len(b) < k {
		k = len(b)
	}
	for i := 0; i < k; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

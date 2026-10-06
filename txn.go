package objtree

import (
	"errors"
	"fmt"
	"slices"
)

// OpKind is the kind of a transactional operation.
type OpKind int

const (
	// OpSet replaces the node at Op.Path with a leaf holding Op.Value.
	OpSet OpKind = iota
	// OpDelete removes the node at Op.Path.
	OpDelete
)

// Op is a single operation of a transaction.
type Op struct {
	Kind  OpKind
	Path  []string
	Value any // only for OpSet
}

// ErrConflict is returned by Apply when the operations of a transaction
// conflict with each other.
var ErrConflict = errors.New("objtree: conflicting operations")

// SetOp is a shorthand for an OpSet operation.
func SetOp(path []string, value any) Op { return Op{Kind: OpSet, Path: path, Value: value} }

// DeleteOp is a shorthand for an OpDelete operation.
func DeleteOp(path []string) Op { return Op{Kind: OpDelete, Path: path} }

// Apply applies a multi-path transaction and returns the new root.
//
// The affected paths are merged into a single path trie before the tree
// is rewritten, so operations sharing common ancestors copy each shared
// ancestor exactly once and never overwrite each other's changes. Every
// subtree not touched by any operation keeps its identity.
//
// A transaction is rejected with ErrConflict when a delete targets the
// same path as, or an ancestor of, a set (deleting a subtree while
// modifying a path inside it), or when two sets target nested paths
// (the outer set would silently discard the inner one). Repeated
// operations of the same kind on the identical path are folded, last
// one wins; a delete subsumes deletes below it.
//
// An empty transaction returns the input root unchanged (same pointer).
func Apply(root *Node, ops ...Op) (*Node, error) {
	if len(ops) == 0 {
		return root, nil
	}
	effective, err := normalize(ops)
	if err != nil {
		return nil, err
	}
	return applyAt(root, effective), nil
}

// normalize folds duplicate paths, drops subsumed deletes and validates
// the remaining operations for conflicts.
func normalize(ops []Op) ([]Op, error) {
	// Fold operations of the same kind on the identical path, last one
	// wins, keeping the order of first occurrence. A delete and a set on
	// the same path are kept and reported as a conflict below.
	effective := make([]Op, 0, len(ops))
	for _, op := range ops {
		replaced := false
		for i := range effective {
			if effective[i].Kind == op.Kind && slices.Equal(effective[i].Path, op.Path) {
				effective[i] = op
				replaced = true
				break
			}
		}
		if !replaced {
			effective = append(effective, op)
		}
	}

	// A delete subsumes any delete strictly below it.
	kept := effective[:0]
	for _, op := range effective {
		if op.Kind == OpDelete && hasAncestorDelete(effective, op.Path) {
			continue
		}
		kept = append(kept, op)
	}
	effective = kept

	// Conflict detection on the remaining operations.
	for i, a := range effective {
		for j, b := range effective {
			if i == j {
				continue
			}
			switch {
			case a.Kind == OpDelete && b.Kind == OpSet && isPrefix(a.Path, b.Path):
				return nil, fmt.Errorf("%w: delete of %q conflicts with set of %q",
					ErrConflict, a.Path, b.Path)
			case a.Kind == OpSet && b.Kind == OpSet && isProperPrefix(a.Path, b.Path):
				return nil, fmt.Errorf("%w: set of %q conflicts with set of nested path %q",
					ErrConflict, a.Path, b.Path)
			}
		}
	}
	return effective, nil
}

func hasAncestorDelete(ops []Op, path []string) bool {
	for _, other := range ops {
		if other.Kind == OpDelete && isProperPrefix(other.Path, path) {
			return true
		}
	}
	return false
}

// applyAt rewrites the subtree at root according to ops, whose paths are
// relative to root. It returns nil when the subtree is deleted. Nodes
// that no operation reaches are returned by pointer, unmodified.
//
// Precondition: ops is non-empty and already normalized.
func applyAt(root *Node, ops []Op) *Node {
	// An operation targeting this node directly can only be the single
	// remaining operation: normalization rejects anything nested below it.
	for _, op := range ops {
		if len(op.Path) == 0 {
			if op.Kind == OpDelete {
				return nil
			}
			return &Node{Value: op.Value}
		}
	}

	// Group the operations by their first path segment: this is the merge
	// step that makes every shared ancestor be copied exactly once.
	groups := make(map[string][]Op)
	order := make([]string, 0, len(ops))
	for _, op := range ops {
		head := op.Path[0]
		if _, ok := groups[head]; !ok {
			order = append(order, head)
		}
		groups[head] = append(groups[head], Op{Kind: op.Kind, Path: op.Path[1:], Value: op.Value})
	}

	var children map[string]*Node
	if root != nil {
		children = cloneChildren(root.Children)
	} else {
		children = make(map[string]*Node, len(order))
	}
	changed := false
	for _, head := range order {
		var old *Node
		if root != nil {
			old = root.Children[head]
		}
		nw := applyAt(old, groups[head])
		switch {
		case nw == nil:
			if _, ok := children[head]; ok {
				delete(children, head)
				changed = true
			}
		case nw != old:
			children[head] = nw
			changed = true
		}
	}
	if !changed {
		return root
	}
	var value any
	if root != nil {
		value = root.Value
	}
	return &Node{Value: value, Children: children}
}

// isPrefix reports whether a is a prefix of b (including equality).
func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isProperPrefix reports whether a is a proper (strict) prefix of b.
func isProperPrefix(a, b []string) bool {
	return len(a) < len(b) && isPrefix(a, b)
}

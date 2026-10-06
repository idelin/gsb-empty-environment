package objtree_test

// Deep-copy reference model: every operation is applied by mutating a
// full deep copy of the tree. The persistent implementation under test
// must produce a structurally equal result while sharing untouched
// subtrees.

import (
	"slices"
	"testing"

	"objtree"
)

// refApply applies one operation to root by mutation and returns the
// (possibly new) root. Callers must pass a deep copy they own.
func refApply(root *objtree.Node, op objtree.Op) *objtree.Node {
	switch op.Kind {
	case objtree.OpSet:
		return refSet(root, op.Path, op.Value)
	case objtree.OpDelete:
		return refDelete(root, op.Path)
	}
	panic("unknown op kind")
}

func refSet(root *objtree.Node, path []string, value any) *objtree.Node {
	if len(path) == 0 {
		return &objtree.Node{Value: value}
	}
	if root == nil {
		root = &objtree.Node{}
	}
	if root.Children == nil {
		root.Children = make(map[string]*objtree.Node)
	}
	root.Children[path[0]] = refSet(root.Children[path[0]], path[1:], value)
	return root
}

func refDelete(root *objtree.Node, path []string) *objtree.Node {
	if root == nil {
		return nil
	}
	if len(path) == 0 {
		return nil
	}
	if c, ok := root.Children[path[0]]; ok {
		if nc := refDelete(c, path[1:]); nc == nil {
			delete(root.Children, path[0])
		} else {
			root.Children[path[0]] = nc
		}
	}
	return root
}

// refApplyAll is the reference model for a whole transaction: deep-copy
// the root, then apply each operation in order by mutation.
func refApplyAll(root *objtree.Node, ops []objtree.Op) *objtree.Node {
	m := objtree.Clone(root)
	for _, op := range foldSameKindOps(ops) {
		m = refApply(m, op)
	}
	return m
}

// foldSameKindOps mirrors the transaction contract: repeated operations
// of the same kind on the identical path are folded, last one wins.
func foldSameKindOps(ops []objtree.Op) []objtree.Op {
	effective := make([]objtree.Op, 0, len(ops))
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
	return effective
}

// assertShared verifies that every node of the original tree that is not
// an ancestor of (or target of) an operation path is shared by pointer
// with the new tree.
func assertShared(t *testing.T, orig, got *objtree.Node, ops []objtree.Op) {
	t.Helper()
	walk(t, orig, got, nil, ops)
}

func walk(t *testing.T, orig, got *objtree.Node, path []string, ops []objtree.Op) {
	t.Helper()
	if orig == nil || got == nil {
		return
	}
	if !onAnyOpPath(path, ops) {
		if orig != got {
			t.Errorf("path %v: untouched subtree not shared (identity lost)", path)
		}
		return
	}
	for k, oc := range orig.Children {
		if gc, ok := got.Children[k]; ok {
			walk(t, oc, gc, append(append([]string{}, path...), k), ops)
		}
	}
}

// onAnyOpPath reports whether path is an ancestor-or-self of some
// operation path, i.e. whether the node at path is allowed to be copied.
func onAnyOpPath(path []string, ops []objtree.Op) bool {
	for _, op := range ops {
		if isPrefixOf(path, op.Path) {
			return true
		}
	}
	return false
}

func isPrefixOf(a, b []string) bool {
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

// sampleTree builds a fixed tree used by several tests:
//
//	root
//	├── a
//	│   ├── b
//	│   │   ├── c: 1
//	│   │   └── d: 2
//	│   └── e: 3
//	└── x: 4
func sampleTree() *objtree.Node {
	return objtree.Internal(map[string]*objtree.Node{
		"a": objtree.Internal(map[string]*objtree.Node{
			"b": objtree.Internal(map[string]*objtree.Node{
				"c": objtree.Leaf(1),
				"d": objtree.Leaf(2),
			}),
			"e": objtree.Leaf(3),
		}),
		"x": objtree.Leaf(4),
	})
}

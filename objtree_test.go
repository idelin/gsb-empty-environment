package objtree

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// --- reference model: mutate a deep copy sequentially ---

func deepCopy(n *Node) *Node {
	if n == nil {
		return nil
	}
	c := &Node{Value: n.Value}
	if n.Children != nil {
		c.Children = make(map[string]*Node, len(n.Children))
		for k, v := range n.Children {
			c.Children[k] = deepCopy(v)
		}
	}
	return c
}

func modelSet(root *Node, path []string, child *Node) *Node {
	if len(path) == 0 {
		return deepCopy(child)
	}
	if root == nil || root.Children == nil {
		// descending into an absent node or a leaf replaces it with a branch
		root = &Node{Children: make(map[string]*Node)}
	}
	root.Children[path[0]] = modelSet(root.Children[path[0]], path[1:], child)
	return root
}

func modelDelete(root *Node, path []string) *Node {
	if root == nil {
		return nil
	}
	if len(path) == 0 {
		return nil
	}
	if len(path) == 1 {
		delete(root.Children, path[0])
		return root
	}
	if c, ok := root.Children[path[0]]; ok {
		next := modelDelete(c, path[1:])
		if next == nil {
			delete(root.Children, path[0])
		} else {
			root.Children[path[0]] = next
		}
	}
	return root
}

// modelApply applies ops one by one to a deep copy of root.
func modelApply(root *Node, ops []Op) *Node {
	// Canonical order: ancestors before descendants, so a replace at a
	// shallower path happens first and deeper ops merge on top of it.
	sorted := append([]Op(nil), ops...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i].Path) < len(sorted[j].Path)
	})
	m := deepCopy(root)
	for _, op := range sorted {
		if op.Set {
			m = modelSet(m, op.Path, op.Value)
		} else {
			m = modelDelete(m, op.Path)
		}
	}
	return m
}

// snapshot flattens a tree into a plain value for DeepEqual comparison.
func snapshot(n *Node) interface{} {
	if n == nil {
		return nil
	}
	if n.Children == nil {
		return n.Value
	}
	m := make(map[string]interface{}, len(n.Children))
	for k, v := range n.Children {
		m[k] = snapshot(v)
	}
	return m
}

// --- helpers ---

func sampleTree() *Node {
	return Branch(map[string]*Node{
		"a": Branch(map[string]*Node{
			"b": Leaf(1),
			"c": Branch(map[string]*Node{"d": Leaf(2)}),
		}),
		"e": Leaf(3),
	})
}

func mustTx(t *testing.T, root *Node, ops []Op) *Node {
	t.Helper()
	next, err := ApplyTransaction(root, ops)
	if err != nil {
		t.Fatalf("ApplyTransaction: %v", err)
	}
	return next
}

// assertShared verifies that every path of oldTree that is untouched by ops
// resolves to the identical pointer in newTree.
func assertShared(t *testing.T, oldTree, newTree *Node, ops []Op) {
	t.Helper()
	var walk func(n *Node, path []string)
	walk = func(n *Node, path []string) {
		if n == nil {
			return
		}
		untouched := true
		for _, op := range ops {
			// path is an ancestor-or-self of the op, or under a set op
			if isPrefix(path, op.Path) || isStrictPrefix(op.Path, path) {
				untouched = false
				break
			}
		}
		if untouched {
			if got := Get(newTree, path); got != n {
				t.Fatalf("path %v: identity not preserved (got %p, want %p)", path, got, n)
			}
			return
		}
		for k, v := range n.Children {
			walk(v, append(append([]string{}, path...), k))
		}
	}
	walk(oldTree, nil)
}

func isPrefix(prefix, path []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, seg := range prefix {
		if path[i] != seg {
			return false
		}
	}
	return true
}

// --- tests ---

func TestSetCopiesOnlyAncestors(t *testing.T) {
	root := sampleTree()
	sharedC := root.Children["a"].Children["c"]
	sharedE := root.Children["e"]

	next := Set(root, []string{"a", "b"}, Leaf(42))

	if next == root {
		t.Fatal("root must be copied")
	}
	if next.Children["a"] == root.Children["a"] {
		t.Fatal("ancestor /a must be copied")
	}
	if got := Get(next, []string{"a", "b"}); got.Value != 42 {
		t.Fatalf("new value = %v, want 42", got.Value)
	}
	if next.Children["a"].Children["c"] != sharedC {
		t.Fatal("sibling subtree /a/c must keep identity")
	}
	if next.Children["e"] != sharedE {
		t.Fatal("sibling subtree /e must keep identity")
	}
	// original untouched
	if got := Get(root, []string{"a", "b"}); got.Value != 1 {
		t.Fatalf("original mutated: %v", got.Value)
	}
}

func TestDeleteSharesUntouched(t *testing.T) {
	root := sampleTree()
	next := Delete(root, []string{"a", "c", "d"})
	if Get(next, []string{"a", "c", "d"}) != nil {
		t.Fatal("node not deleted")
	}
	if next.Children["e"] != root.Children["e"] {
		t.Fatal("/e must keep identity")
	}
	if next.Children["a"].Children["b"] != root.Children["a"].Children["b"] {
		t.Fatal("/a/b must keep identity")
	}
	// deleting a missing path returns the same root
	if got := Delete(root, []string{"nope", "nada"}); got != root {
		t.Fatal("delete of missing path must return root unchanged")
	}
}

func TestTransactionMergesCommonAncestors(t *testing.T) {
	root := sampleTree()
	ops := []Op{
		{Path: []string{"a", "b"}, Set: true, Value: Leaf(10)},
		{Path: []string{"a", "c", "d"}, Set: true, Value: Leaf(20)},
	}
	next := mustTx(t, root, ops)
	if got := Get(next, []string{"a", "b"}); got.Value != 10 {
		t.Fatalf("/a/b = %v, want 10", got.Value)
	}
	if got := Get(next, []string{"a", "c", "d"}); got.Value != 20 {
		t.Fatalf("/a/c/d = %v, want 20", got.Value)
	}
	// /a copied exactly once: both changes live under the same /a node
	a := next.Children["a"]
	if a.Children["b"].Value != 10 || a.Children["c"].Children["d"].Value != 20 {
		t.Fatal("sibling updates did not merge under one ancestor copy")
	}
	if next.Children["e"] != root.Children["e"] {
		t.Fatal("/e must keep identity")
	}
}

func TestTransactionConflictRejected(t *testing.T) {
	root := sampleTree()
	cases := [][]Op{
		{{Path: []string{"a"}}, {Path: []string{"a", "b"}, Set: true, Value: Leaf(1)}},
		{{Path: []string{"a"}}, {Path: []string{"a", "b"}}},
		{{Path: []string{"a", "b"}, Set: true, Value: Leaf(1)}, {Path: []string{"a"}}},
	}
	for i, ops := range cases {
		if _, err := ApplyTransaction(root, ops); err == nil {
			t.Fatalf("case %d: expected conflict error", i)
		}
	}
}

func TestEmptyTransaction(t *testing.T) {
	root := sampleTree()
	next, err := ApplyTransaction(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next != root {
		t.Fatal("empty transaction must return the identical root")
	}
	// transaction with only no-op deletes also preserves identity
	next = mustTx(t, root, []Op{{Path: []string{"missing", "path"}}})
	if next != root {
		t.Fatal("no-op transaction must return the identical root")
	}
}

func TestAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	keys := []string{"a", "b", "c", "d"}
	randPath := func() []string {
		n := rng.Intn(4)
		p := make([]string, n)
		for i := range p {
			p[i] = keys[rng.Intn(len(keys))]
		}
		return p
	}
	for trial := 0; trial < 200; trial++ {
		// random initial tree
		var root *Node
		for i := 0; i < 8; i++ {
			root = Set(root, randPath(), Leaf(rng.Intn(100)))
		}
		// random non-conflicting ops
		var ops []Op
		for i := 0; i < 6; i++ {
			p := randPath()
			conflict := false
			for _, o := range ops {
				if !o.Set && isStrictPrefix(o.Path, p) {
					conflict = true
				}
			}
			if conflict {
				continue
			}
			del := rng.Intn(2) == 1
			if del {
				for _, o := range ops {
					if isStrictPrefix(p, o.Path) {
						conflict = true // delete ancestor of an existing op
					}
				}
			}
			if conflict {
				continue
			}
			if del {
				ops = append(ops, Op{Path: p})
			} else {
				ops = append(ops, Op{Path: p, Set: true, Value: Leaf(rng.Intn(100))})
			}
		}
		next, err := ApplyTransaction(root, ops)
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		want := snapshot(modelApply(root, ops))
		if got := snapshot(next); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: snapshot mismatch\nops:  %v\nroot: %v\ngot:  %v\nwant: %v", trial, ops, snapshot(root), got, want)
		}
		assertShared(t, root, next, ops)
	}
}

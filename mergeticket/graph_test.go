package mergeticket

import "testing"

func TestGraph_ContentAddressingAndAncestry(t *testing.T) {
	g := NewGraph()
	tree1 := g.AddTree(map[string]string{"a": "1"})
	tree2 := g.AddTree(map[string]string{"a": "1"})
	if tree1 != tree2 {
		t.Fatal("identical file sets must hash to the same tree")
	}

	root, err := g.AddCommit("", tree1)
	if err != nil {
		t.Fatalf("root commit: %v", err)
	}
	if _, err := g.AddCommit("missing", tree1); err == nil {
		t.Fatal("commit with unknown parent must fail")
	}

	nextTree := g.AddTree(map[string]string{"a": "2"})
	next, err := g.AddCommit(root, nextTree)
	if err != nil {
		t.Fatalf("next commit: %v", err)
	}
	// 同一棵树挂在不同父提交下，提交哈希不同。
	alt, err := g.AddCommit(next, tree1)
	if err != nil {
		t.Fatalf("alt commit: %v", err)
	}
	sameTreeOtherParent, _ := g.AddCommit(root, tree1)
	if sameTreeOtherParent == root || alt == root {
		t.Fatal("commit hash must depend on the parent, not only the tree")
	}

	if err := g.SetRef("main", root); err != nil {
		t.Fatalf("set ref: %v", err)
	}
	if err := g.SetRef("main", next); err != nil {
		t.Fatalf("fast-forward ref: %v", err)
	}
	if err := g.SetRef("main", root); err == nil {
		t.Fatal("non-fast-forward ref update must be rejected")
	}
	if !g.IsAncestor(root, next) || g.IsAncestor(next, root) {
		t.Fatal("ancestry walk is wrong")
	}
	if !g.IsAncestor(next, next) {
		t.Fatal("a commit is its own ancestor")
	}
}

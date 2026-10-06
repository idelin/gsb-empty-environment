package snapiter

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// reference is a plain recursive pre-order traversal used as ground truth.
func reference(root *Node) []Entry {
	var out []Entry
	var walk func(n *Node, path []int)
	walk = func(n *Node, path []int) {
		cp := append([]int(nil), path...)
		out = append(out, Entry{Path: cp, Node: n})
		for i, c := range n.Children {
			walk(c, append(cp, i))
		}
	}
	if root != nil {
		walk(root, nil)
	}
	return out
}

func entriesEqual(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Node != b[i].Node || !reflect.DeepEqual(a[i].Path, b[i].Path) {
			return false
		}
	}
	return true
}

// collect drains the tree in chunks of (maxNodes, maxBytes), resuming via
// tokens, and returns all emitted entries.
func collect(t *testing.T, tree *Tree, maxNodes, maxBytes int) []Entry {
	t.Helper()
	var all []Entry
	it := tree.NewIterator()
	for {
		entries, token, done := it.Next(maxNodes, maxBytes)
		all = append(all, entries...)
		if done {
			return all
		}
		if token == "" {
			t.Fatalf("incomplete traversal returned empty token")
		}
		var err error
		it, err = tree.Resume(token)
		if err != nil {
			t.Fatalf("Resume(%q): %v", token, err)
		}
	}
}

// sampleTree builds a small DAG with a shared node:
//
//	root
//	├── a
//	│   └── shared
//	└── b
//	    └── shared   (same pointer, emitted again on this path)
func sampleTree() (*Tree, *Node) {
	shared := &Node{ID: "shared", Data: []byte("payload")}
	a := &Node{ID: "a", Data: []byte("x"), Children: []*Node{shared}}
	b := &Node{ID: "b", Data: []byte("yy"), Children: []*Node{shared}}
	root := &Node{ID: "root", Children: []*Node{a, b}}
	return NewTree(root), shared
}

func TestFullTraversalMatchesReference(t *testing.T) {
	tree, _ := sampleTree()
	got := collect(t, tree, 1<<30, 1<<30)
	if !entriesEqual(got, reference(tree.Root())) {
		t.Fatalf("full traversal mismatch: %v", got)
	}
}

func TestNodeBudgetSplits(t *testing.T) {
	tree, _ := sampleTree()
	want := reference(tree.Root())
	for budget := 1; budget <= len(want)+1; budget++ {
		got := collect(t, tree, budget, 1<<30)
		if !entriesEqual(got, want) {
			t.Fatalf("node budget %d: got %v want %v", budget, got, want)
		}
	}
}

func TestByteBudgetSplits(t *testing.T) {
	tree, _ := sampleTree()
	want := reference(tree.Root())
	total := 0
	for _, e := range want {
		total += e.Node.size()
	}
	for budget := 0; budget <= total+1; budget++ {
		got := collect(t, tree, 1<<30, budget)
		if !entriesEqual(got, want) {
			t.Fatalf("byte budget %d: got %v want %v", budget, got, want)
		}
	}
}

func TestCombinedBudgetSplits(t *testing.T) {
	tree, _ := sampleTree()
	want := reference(tree.Root())
	for nodes := 1; nodes <= len(want); nodes++ {
		for bytes := 0; bytes <= 12; bytes++ {
			got := collect(t, tree, nodes, bytes)
			if !entriesEqual(got, want) {
				t.Fatalf("budget (%d,%d): got %v want %v", nodes, bytes, got, want)
			}
		}
	}
}

func TestEverySplitPoint(t *testing.T) {
	tree, _ := sampleTree()
	want := reference(tree.Root())
	for k := 0; k <= len(want); k++ {
		it := tree.NewIterator()
		first, token, done := it.Next(k, 1<<30)
		if k >= len(want) {
			if !done || !entriesEqual(first, want) {
				t.Fatalf("k=%d: expected completion", k)
			}
			continue
		}
		if done {
			t.Fatalf("k=%d: unexpectedly done", k)
		}
		it2, err := tree.Resume(token)
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		rest, _, done2 := it2.Next(1<<30, 1<<30)
		if !done2 {
			t.Fatalf("k=%d: resume did not finish", k)
		}
		if got := append(first, rest...); !entriesEqual(got, want) {
			t.Fatalf("k=%d: got %v want %v", k, got, want)
		}
	}
}

func TestEmptyBudget(t *testing.T) {
	tree, _ := sampleTree()
	it := tree.NewIterator()
	entries, token, done := it.Next(0, 0)
	if len(entries) != 0 || done || token == "" {
		t.Fatalf("empty budget: entries=%v token=%q done=%v", entries, token, done)
	}
	// Repeated empty-budget calls are idempotent and yield the same token.
	entries2, token2, done2 := it.Next(0, 0)
	if len(entries2) != 0 || done2 || token2 != token {
		t.Fatalf("empty budget not idempotent: %q vs %q", token2, token)
	}
	// Resuming from the token traverses everything exactly once.
	it2, err := tree.Resume(token)
	if err != nil {
		t.Fatal(err)
	}
	var got []Entry
	for {
		entries, tok, done := it2.Next(2, 1<<30)
		got = append(got, entries...)
		if done {
			break
		}
		it2, err = tree.Resume(tok)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !entriesEqual(got, reference(tree.Root())) {
		t.Fatalf("after empty budget: %v", got)
	}
}

func TestEmptyTree(t *testing.T) {
	tree := NewTree(nil)
	it := tree.NewIterator()
	entries, token, done := it.Next(0, 0)
	if entries != nil || token != "" || !done {
		t.Fatalf("empty tree: entries=%v token=%q done=%v", entries, token, done)
	}
}

func TestSharedNodeEmittedPerPath(t *testing.T) {
	tree, shared := sampleTree()
	got := collect(t, tree, 1<<30, 1<<30)
	paths := make(map[string]int)
	sharedCount := 0
	for _, e := range got {
		key := fmt.Sprint(e.Path)
		paths[key]++
		if e.Node == shared {
			sharedCount++
			if len(e.Path) != 2 {
				t.Fatalf("shared node at unexpected path %v", e.Path)
			}
		}
	}
	for p, n := range paths {
		if n != 1 {
			t.Fatalf("path %v emitted %d times", p, n)
		}
	}
	if sharedCount != 2 {
		t.Fatalf("shared node emitted %d times, want 2 (once per path)", sharedCount)
	}
	if len(got) != 5 { // root, a, shared, b, shared
		t.Fatalf("got %d entries, want 5", len(got))
	}
}

func TestByteBudgetAccounting(t *testing.T) {
	tree, _ := sampleTree()
	const budget = 4
	it := tree.NewIterator()
	for {
		entries, token, done := it.Next(1<<30, budget)
		used := 0
		for _, e := range entries {
			used += e.Node.size()
		}
		if len(entries) > 1 && used > budget {
			t.Fatalf("chunk used %d bytes > budget %d", used, budget)
		}
		if done {
			break
		}
		var err error
		it, err = tree.Resume(token)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestStaleTokenRejected(t *testing.T) {
	tree, _ := sampleTree()
	it := tree.NewIterator()
	_, token, done := it.Next(2, 1<<30)
	if done {
		t.Fatal("expected incomplete traversal")
	}
	mutations := []func(){
		func() { tree.AddChild(tree.Root(), &Node{ID: "c"}) },
		func() { tree.RemoveChildAt(tree.Root(), 2) },
		func() { tree.SetData(tree.Root(), []byte("changed")) },
		func() { tree.ReplaceRoot(tree.Root()) },
	}
	for i, mutate := range mutations {
		mutate()
		if _, err := tree.Resume(token); err != ErrStaleToken {
			t.Fatalf("mutation %d: got %v, want ErrStaleToken", i, err)
		}
	}
}

func TestForeignAndMalformedTokens(t *testing.T) {
	tree, _ := sampleTree()
	other := NewTree(&Node{ID: "other"})
	it := tree.NewIterator()
	_, token, _ := it.Next(1, 1<<30)
	if _, err := other.Resume(token); err != ErrStaleToken {
		t.Fatalf("foreign tree: got %v, want ErrStaleToken", err)
	}
	for _, bad := range []string{"", "snap1", "snap2.x.0.0.", "snap1.x.y.0.", "snap1.x.0.2.", "snap1.x.0.1.9,", "snap1.x.0.1.-1"} {
		if _, err := tree.Resume(bad); err != ErrInvalidToken && err != ErrStaleToken {
			t.Fatalf("token %q: got %v", bad, err)
		}
	}
	// Out-of-range path for the current version.
	it2 := tree.NewIterator()
	_, tok, _ := it2.Next(1, 1<<30) // after root
	parts := []byte(tok)
	parts[len(parts)-1] = '9'
	if _, err := tree.Resume(string(parts)); err != ErrInvalidToken {
		t.Fatalf("out-of-range path: got %v, want ErrInvalidToken", err)
	}
}

// randomDAG builds an acyclic graph with shared nodes: node i picks random
// children among nodes created before it, always including node i-1 so the
// last node reaches all others.
func randomDAG(r *rand.Rand, n int) *Node {
	nodes := make([]*Node, n)
	for i := 0; i < n; i++ {
		data := make([]byte, r.Intn(6))
		if _, err := r.Read(data); err != nil {
			panic(err)
		}
		nodes[i] = &Node{ID: fmt.Sprintf("n%d", i), Data: data}
		if i == 0 {
			continue
		}
		nodes[i].Children = append(nodes[i].Children, nodes[i-1])
		for j := 0; j < i; j++ {
			if r.Intn(3) == 0 {
				nodes[i].Children = append(nodes[i].Children, nodes[j])
			}
		}
		r.Shuffle(len(nodes[i].Children), func(a, b int) {
			nodes[i].Children[a], nodes[i].Children[b] = nodes[i].Children[b], nodes[i].Children[a]
		})
	}
	return nodes[n-1]
}

func TestRandomBudgetSplitsAgainstReference(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		root := randomDAG(r, 2+r.Intn(8))
		tree := NewTree(root)
		want := reference(root)
		if len(want) > 2000 { // keep shared-node explosions small
			continue
		}
		for split := 0; split < 10; split++ {
			maxNodes := 1 + r.Intn(len(want)+1)
			maxBytes := r.Intn(20)
			var got []Entry
			it := tree.NewIterator()
			for {
				entries, token, done := it.Next(maxNodes, maxBytes)
				got = append(got, entries...)
				if done {
					break
				}
				it2, err := tree.Resume(token)
				if err != nil {
					t.Fatalf("trial %d split %d: %v", trial, split, err)
				}
				it = it2
			}
			if !entriesEqual(got, want) {
				t.Fatalf("trial %d split %d budget(%d,%d):\ngot  %v\nwant %v",
					trial, split, maxNodes, maxBytes, got, want)
			}
		}
	}
}

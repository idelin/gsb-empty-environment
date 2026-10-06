package snapshot

import (
	"math/rand"
	"reflect"
	"testing"
)

// drain runs the traversal to completion, resuming via tokens whenever the
// budget is exhausted, and returns all emitted paths in order.
func drain(t *testing.T, tree *Tree, b Budget) []string {
	t.Helper()
	var all []string
	n := 0
	it := NewIterator(tree, b)
	for {
		for {
			p, ok := it.Next()
			if !ok {
				break
			}
			all = append(all, p)
		}
		token, ok := it.Token()
		if !ok {
			return all
		}
		if n == len(all) {
			t.Fatalf("no progress with budget %+v", b)
		}
		n = len(all)
		var err error
		it, err = Resume(tree, token, b)
		if err != nil {
			t.Fatalf("Resume(%q): %v", token, err)
		}
	}
}

// buildDAG builds a random DAG (children only reference already-created
// nodes, so sharing is common and cycles are impossible).
func buildDAG(r *rand.Rand, numNodes, maxChildren int) *Node {
	pool := []*Node{{Name: "n0"}}
	for i := 1; i < numNodes; i++ {
		n := &Node{Name: "n" + itoa(i)}
		pool = append(pool, n)
		// Attach the new node under a random existing node.
		parent := pool[r.Intn(len(pool)-1)]
		parent.Children = append(parent.Children, n)
	}
	// Add extra edges to create shared nodes. Edges only ever point from
	// a lower index to a higher one, keeping the graph acyclic.
	for j := 0; j+1 < numNodes; j++ {
		if r.Intn(2) == 0 {
			continue
		}
		parent := pool[j]
		child := pool[j+1+r.Intn(numNodes-j-1)]
		if len(parent.Children) < maxChildren {
			parent.Children = append(parent.Children, child)
		}
	}
	return pool[0]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestArbitraryBudgetSplitsMatchFullTraversal(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		root := buildDAG(r, 5+r.Intn(20), 4)
		tree := NewTree(root)
		full := drain(t, tree, Unlimited)
		if len(full) == 0 {
			t.Fatal("full traversal emitted nothing")
		}
		totalBytes, maxPath := 0, 0
		for _, p := range full {
			totalBytes += len(p)
			if len(p) > maxPath {
				maxPath = len(p)
			}
		}
		for split := 0; split < 30; split++ {
			// Budgets are hard caps: MaxBytes must fit the longest
			// remaining path for the traversal to progress. Zero
			// budgets are covered by TestEmptyBudget.
			b := Budget{
				MaxNodes: 1 + r.Intn(len(full)+1),
				MaxBytes: maxPath + r.Intn(totalBytes-maxPath+1),
			}
			if r.Intn(4) == 0 {
				b.MaxNodes = -1 // unlimited nodes, bytes only
			}
			if r.Intn(4) == 0 {
				b.MaxBytes = -1 // unlimited bytes, nodes only
			}
			got := drain(t, tree, b)
			if !reflect.DeepEqual(got, full) {
				t.Fatalf("trial %d budget %+v:\n got %q\nwant %q", trial, b, got, full)
			}
		}
	}
}

func TestEmptyBudget(t *testing.T) {
	shared := &Node{Name: "shared"}
	root := &Node{Name: "root", Children: []*Node{
		{Name: "a", Children: []*Node{shared}},
		{Name: "b", Children: []*Node{shared}},
	}}
	tree := NewTree(root)

	it := NewIterator(tree, Budget{MaxNodes: 0, MaxBytes: 0})
	if p, ok := it.Next(); ok {
		t.Fatalf("empty budget emitted %q", p)
	}
	token, ok := it.Token()
	if !ok || token == "" {
		t.Fatal("empty budget must yield a resume token")
	}
	resumed, err := Resume(tree, token, Unlimited)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	var got []string
	for {
		p, ok := resumed.Next()
		if !ok {
			break
		}
		got = append(got, p)
	}
	want := []string{"/root", "/root/a", "/root/a/shared", "/root/b", "/root/b/shared"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSharedNodesEmittedPerPath(t *testing.T) {
	shared := &Node{Name: "s", Children: []*Node{{Name: "leaf"}}}
	root := &Node{Name: "r", Children: []*Node{
		{Name: "x", Children: []*Node{shared}},
		{Name: "y", Children: []*Node{shared}},
		shared, // shared also directly under root
	}}
	tree := NewTree(root)
	got := drain(t, tree, Unlimited)
	want := []string{
		"/r",
		"/r/x", "/r/x/s", "/r/x/s/leaf",
		"/r/y", "/r/y/s", "/r/y/s/leaf",
		"/r/s", "/r/s/leaf",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStaleTokenRejected(t *testing.T) {
	root := &Node{Name: "root", Children: []*Node{{Name: "a"}, {Name: "b"}}}
	tree := NewTree(root)

	it := NewIterator(tree, Budget{MaxNodes: 2, MaxBytes: -1})
	it.Next()
	it.Next()
	token, ok := it.Token()
	if !ok {
		t.Fatal("expected a token")
	}

	tree.Update(func() {
		root.Children = append(root.Children, &Node{Name: "c"})
	})

	if _, err := Resume(tree, token, Unlimited); err != ErrStaleToken {
		t.Fatalf("got %v, want ErrStaleToken", err)
	}

	// A fresh token taken after the mutation still works.
	it2 := NewIterator(tree, Budget{MaxNodes: 1, MaxBytes: -1})
	it2.Next()
	token2, _ := it2.Token()
	if _, err := Resume(tree, token2, Unlimited); err != nil {
		t.Fatalf("fresh token rejected: %v", err)
	}
}

func TestResumeDoesNotRepeatNodes(t *testing.T) {
	// Split at every possible node boundary and byte boundary on a shared
	// node tree; each split must reproduce the full traversal exactly.
	shared := &Node{Name: "s"}
	root := &Node{Name: "r", Children: []*Node{
		{Name: "a", Children: []*Node{shared, {Name: "t"}}},
		{Name: "b", Children: []*Node{shared}},
	}}
	tree := NewTree(root)
	full := drain(t, tree, Unlimited)
	totalBytes, maxPath := 0, 0
	for _, p := range full {
		totalBytes += len(p)
		if len(p) > maxPath {
			maxPath = len(p)
		}
	}
	for maxNodes := 1; maxNodes <= len(full); maxNodes++ {
		for maxBytes := maxPath; maxBytes <= totalBytes; maxBytes++ {
			got := drain(t, tree, Budget{MaxNodes: maxNodes, MaxBytes: maxBytes})
			if !reflect.DeepEqual(got, full) {
				t.Fatalf("budget {%d, %d}: got %q, want %q", maxNodes, maxBytes, got, full)
			}
		}
	}
}

func TestNilRoot(t *testing.T) {
	tree := NewTree(nil)
	it := NewIterator(tree, Unlimited)
	if _, ok := it.Next(); ok {
		t.Fatal("nil root must emit nothing")
	}
	if _, ok := it.Token(); ok {
		t.Fatal("nil root must not yield a token")
	}
}

func TestEmptyBudgetTokenIsStable(t *testing.T) {
	root := &Node{Name: "root", Children: []*Node{{Name: "a"}}}
	tree := NewTree(root)
	empty := Budget{MaxNodes: 0, MaxBytes: 0}

	it := NewIterator(tree, empty)
	token1, ok := it.Token()
	if !ok {
		t.Fatal("expected a token")
	}
	// Resuming with an empty budget emits nothing and re-issues the
	// identical token (same version, same stack).
	it2, err := Resume(tree, token1, empty)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if p, ok := it2.Next(); ok {
		t.Fatalf("empty budget emitted %q", p)
	}
	token2, ok := it2.Token()
	if !ok || token1 != token2 {
		t.Fatalf("token changed across empty resume: %q -> %q", token1, token2)
	}
	// Resuming with a real budget completes the traversal.
	got := drain(t, tree, Unlimited)
	want := []string{"/root", "/root/a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

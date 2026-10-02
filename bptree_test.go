package bptree

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// TestModelRandomOps cross-checks the tree against a map model under random
// sequential insert/delete workloads, and inspects invariants throughout.
func TestModelRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tr := New()
	model := map[int]int{}
	for op := 0; op < 20000; op++ {
		key := rng.Intn(3000)
		if rng.Intn(100) < 60 {
			tr.Insert(key, key*7)
			model[key] = key * 7
		} else {
			got := tr.Delete(key)
			_, want := model[key]
			if got != want {
				t.Fatalf("op %d: Delete(%d)=%v, model says %v", op, key, got, want)
			}
			delete(model, key)
		}
		if op%2000 == 1999 {
			if r := Inspect(tr); !r.OK() {
				t.Fatalf("op %d: inspection failed:\n%s", op, r)
			}
		}
	}
	assertMatchesModel(t, tr, model)
	if r := Inspect(tr); !r.OK() {
		t.Fatalf("final inspection failed:\n%s", r)
	}
}

// TestConcurrentStressInspect hammers the tree with concurrent writers and
// deleters on disjoint key ranges, then runs a full inspection.
func TestConcurrentStressInspect(t *testing.T) {
	tr := New()
	const workers = 8
	const opsPerWorker = 5000
	models := make([]map[int]int, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		models[w] = map[int]int{}
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1000 + w)))
			base := w * 1_000_000 // disjoint key ranges keep the model exact
			for op := 0; op < opsPerWorker; op++ {
				key := base + rng.Intn(20000)
				if rng.Intn(100) < 55 {
					tr.Insert(key, key)
					models[w][key] = key
				} else {
					tr.Delete(key)
					delete(models[w], key)
				}
			}
		}(w)
	}
	wg.Wait()

	model := map[int]int{}
	for _, m := range models {
		for k, v := range m {
			model[k] = v
		}
	}
	assertMatchesModel(t, tr, model)

	r := Inspect(tr)
	if !r.OK() {
		t.Fatalf("inspection after concurrent stress failed:\n%s", r)
	}
	t.Logf("post-stress inspection passed:\n%s", r)
}

// TestDetectRoutingKeyViolation injects the "parent routing key greater than
// left subtree max" corruption and verifies the inspector pinpoints it.
func TestDetectRoutingKeyViolation(t *testing.T) {
	tr := New()
	for k := 0; k < 500; k++ {
		tr.Insert(k, k)
	}
	if r := Inspect(tr); !r.OK() {
		t.Fatalf("clean tree failed inspection:\n%s", r)
	}

	// Find an internal node and corrupt one routing key: set it one above
	// the true max of its left subtree.
	victim, idx := findInternalNode(t, tr)
	trueMax := subtreeMax(victim.children[idx])
	victim.keys[idx] = trueMax + 1

	r := Inspect(tr)
	if r.OK() {
		t.Fatal("inspector failed to detect injected routing-key violation")
	}
	found := false
	for _, v := range r.Violations {
		if v.Rule == "RoutingKeyMismatch" &&
			strings.Contains(v.Detail, "greater than left subtree max") &&
			strings.Contains(v.Detail, fmt.Sprintf("=%d", trueMax+1)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no precise RoutingKeyMismatch violation reported:\n%s", r)
	}
	t.Logf("injected fault located as:\n%s", r)
}

// TestDetectRoutingKeyTooSmall injects a routing key below the left subtree
// max and verifies detection.
func TestDetectRoutingKeyTooSmall(t *testing.T) {
	tr := New()
	for k := 0; k < 500; k++ {
		tr.Insert(k, k)
	}
	victim, idx := findInternalNode(t, tr)
	trueMax := subtreeMax(victim.children[idx])
	victim.keys[idx] = trueMax - 1

	r := Inspect(tr)
	if r.OK() {
		t.Fatal("inspector failed to detect shrunk routing key")
	}
	found := false
	for _, v := range r.Violations {
		if v.Rule == "RoutingKeyMismatch" && strings.Contains(v.Detail, "smaller than left subtree max") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected RoutingKeyMismatch (smaller), got:\n%s", r)
	}
}

// TestDetectLeafChainCycle corrupts the leaf chain into a cycle.
func TestDetectLeafChainCycle(t *testing.T) {
	tr := New()
	for k := 0; k < 200; k++ {
		tr.Insert(k, k)
	}
	leaves := collectLeaves(tr)
	if len(leaves) < 3 {
		t.Fatalf("need at least 3 leaves, got %d", len(leaves))
	}
	// Create a cycle: last leaf points back to an earlier leaf.
	leaves[len(leaves)-1].next = leaves[1]

	r := Inspect(tr)
	assertHasViolation(t, r, "LeafChainCycle")
}

// TestDetectLeafChainPrevBreak corrupts a prev pointer.
func TestDetectLeafChainPrevBreak(t *testing.T) {
	tr := New()
	for k := 0; k < 200; k++ {
		tr.Insert(k, k)
	}
	leaves := collectLeaves(tr)
	if len(leaves) < 2 {
		t.Fatalf("need at least 2 leaves, got %d", len(leaves))
	}
	leaves[1].prev = leaves[0].prev // break the bidirectional closure

	r := Inspect(tr)
	assertHasViolation(t, r, "LeafChainPrev")
}

// TestDetectLeafUnlinked drops a leaf from the chain.
func TestDetectLeafUnlinked(t *testing.T) {
	tr := New()
	for k := 0; k < 200; k++ {
		tr.Insert(k, k)
	}
	leaves := collectLeaves(tr)
	if len(leaves) < 3 {
		t.Fatalf("need at least 3 leaves, got %d", len(leaves))
	}
	// Bypass leaves[1]: leaves[0].next = leaves[2].
	leaves[0].next = leaves[2]
	leaves[2].prev = leaves[0]

	r := Inspect(tr)
	assertHasViolation(t, r, "LeafUnreachable")
}

// TestEmptyAndSingleKey trees pass inspection.
func TestEdgeTrees(t *testing.T) {
	tr := New()
	if r := Inspect(tr); !r.OK() {
		t.Fatalf("empty tree failed:\n%s", r)
	}
	tr.Insert(7, 7)
	if r := Inspect(tr); !r.OK() {
		t.Fatalf("single-key tree failed:\n%s", r)
	}
	tr.Delete(7)
	if r := Inspect(tr); !r.OK() {
		t.Fatalf("emptied tree failed:\n%s", r)
	}
}

func assertMatchesModel(t *testing.T, tr *Tree, model map[int]int) {
	t.Helper()
	got := tr.Scan()
	want := make([]int, 0, len(model))
	for k := range model {
		want = append(want, k)
	}
	sort.Ints(want)
	if len(got) != len(want) {
		t.Fatalf("scan returned %d keys, model has %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scan[%d]=%d, want %d", i, got[i], want[i])
		}
	}
	for k, v := range model {
		gv, ok := tr.Get(k)
		if !ok || gv != v {
			t.Fatalf("Get(%d)=(%d,%v), want (%d,true)", k, gv, ok, v)
		}
	}
}

// findInternalNode returns the first internal node (BFS) with at least one
// routing key, plus the index of the key to corrupt.
func findInternalNode(t *testing.T, tr *Tree) (*Node, int) {
	t.Helper()
	queue := []*Node{tr.root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.leaf {
			continue
		}
		if len(n.keys) > 0 {
			return n, len(n.keys) / 2
		}
		queue = append(queue, n.children...)
	}
	t.Fatal("no internal node with keys found")
	return nil, 0
}

// collectLeaves returns the leaves in chain order.
func collectLeaves(tr *Tree) []*Node {
	n := tr.root
	for !n.leaf {
		n = n.children[0]
	}
	var out []*Node
	for n != nil {
		out = append(out, n)
		n = n.next
	}
	return out
}

func assertHasViolation(t *testing.T, r *Report, rule string) {
	t.Helper()
	if r.OK() {
		t.Fatalf("inspector reported OK, expected violation %q", rule)
	}
	for _, v := range r.Violations {
		if v.Rule == rule {
			t.Logf("detected %q as expected:\n%s", rule, r)
			return
		}
	}
	t.Fatalf("violation %q not found in report:\n%s", rule, r)
}

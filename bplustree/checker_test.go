package bplustree

import (
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// hammerTree simulates heavy concurrent writes and deletes against the
// tree (serialized by a mutex, as the tree itself is not thread-safe).
func hammerTree(t *testing.T, order, workers, ops int) *Tree {
	t.Helper()
	tree := New(order)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < ops; i++ {
				key := rng.Intn(2000)
				mu.Lock()
				if rng.Intn(100) < 55 {
					tree.Insert(key, key*10)
				} else {
					tree.Delete(key)
				}
				mu.Unlock()
			}
		}(int64(w)*7919 + 13)
	}
	wg.Wait()
	return tree
}

func hasViolation(r *Report, code string) *Violation {
	for i := range r.Violations {
		if r.Violations[i].Code == code {
			return &r.Violations[i]
		}
	}
	return nil
}

func TestInspectHealthyAfterConcurrentWorkload(t *testing.T) {
	tree := hammerTree(t, 8, 8, 5000)
	rep := Inspect(tree)
	if !rep.OK() {
		t.Fatalf("expected clean report, got:\n%s", rep)
	}
	if rep.KeysChecked == 0 {
		t.Fatal("expected non-empty tree after workload")
	}
	// Spot-check contents against Get.
	n := 0
	for leaf := tree.leftmost(); leaf != nil; leaf = leaf.Next {
		for _, k := range leaf.Keys {
			if _, ok := tree.Get(k); !ok {
				t.Fatalf("key %d in leaf chain but Get misses", k)
			}
			n++
		}
	}
	if n != tree.Size() {
		t.Fatalf("chain walk counted %d keys, Size()=%d", n, tree.Size())
	}
	t.Logf("healthy tree: %s", strings.Split(rep.String(), "\n")[0])
}

func findInternalWithChildren(t *testing.T, n *Node, minChildren int) *Node {
	if n.IsLeaf {
		return nil
	}
	if len(n.Children) >= minChildren {
		return n
	}
	for _, c := range n.Children {
		if got := findInternalWithChildren(t, c, minChildren); got != nil {
			return got
		}
	}
	return nil
}

func TestDetectsRouteKeyGreaterThanLeftMax(t *testing.T) {
	tree := hammerTree(t, 8, 4, 3000)
	if rep := Inspect(tree); !rep.OK() {
		t.Fatalf("pre-injection tree not healthy:\n%s", rep)
	}
	victim := findInternalWithChildren(t, tree.Root, 2)
	if victim == nil {
		t.Fatal("no internal node found")
	}
	// Inject: routing key keys[0] becomes larger than the max key of the
	// left subtree child[0].
	if len(victim.Children[0].Keys) == 0 {
		t.Fatal("empty left subtree")
	}
	leftMax := subtreeMax(victim.Children[0])
	victim.Keys[0] = leftMax + 1000

	rep := Inspect(tree)
	if rep.OK() {
		t.Fatal("inspector missed injected routing-key violation")
	}
	v := hasViolation(rep, codeRouteGTLeftMax)
	if v == nil {
		t.Fatalf("expected %s violation, got:\n%s", codeRouteGTLeftMax, rep)
	}
	if !strings.HasPrefix(v.Path, "root") {
		t.Fatalf("violation path not rooted: %q", v.Path)
	}
	if !strings.Contains(v.Detail, "exceeds max key") {
		t.Fatalf("unhelpful detail: %q", v.Detail)
	}
	t.Logf("caught injected fault: %s", v)
}

func TestDetectsUnsortedKeys(t *testing.T) {
	tree := New(4)
	for i := 0; i < 100; i++ {
		tree.Insert(i, i)
	}
	leaf := tree.leftmost()
	leaf.Keys[0], leaf.Keys[1] = leaf.Keys[1], leaf.Keys[0]
	rep := Inspect(tree)
	if hasViolation(rep, codeUnsorted) == nil {
		t.Fatalf("expected %s, got:\n%s", codeUnsorted, rep)
	}
}

func TestDetectsLeafCycle(t *testing.T) {
	tree := New(4)
	for i := 0; i < 200; i++ {
		tree.Insert(i, i)
	}
	// Link the last leaf back to the head: a cycle.
	head := tree.leftmost()
	tail := head
	for tail.Next != nil {
		tail = tail.Next
	}
	tail.Next = head.Next
	rep := Inspect(tree)
	if hasViolation(rep, codeLeafCycle) == nil {
		t.Fatalf("expected %s, got:\n%s", codeLeafCycle, rep)
	}
}

func TestDetectsBrokenPrevPointer(t *testing.T) {
	tree := New(4)
	for i := 0; i < 200; i++ {
		tree.Insert(i, i)
	}
	second := tree.leftmost().Next
	if second == nil {
		t.Fatal("need at least two leaves")
	}
	second.Prev = nil
	rep := Inspect(tree)
	if hasViolation(rep, codeLeafPrev) == nil {
		t.Fatalf("expected %s, got:\n%s", codeLeafPrev, rep)
	}
}

func TestDetectsDetachedLeaf(t *testing.T) {
	tree := New(4)
	for i := 0; i < 200; i++ {
		tree.Insert(i, i)
	}
	// Detach the second leaf from the forward chain.
	head := tree.leftmost()
	head.Next = head.Next.Next
	rep := Inspect(tree)
	if hasViolation(rep, codeLeafMissing) == nil && hasViolation(rep, codeLeafPrev) == nil {
		t.Fatalf("expected leaf-chain violation, got:\n%s", rep)
	}
}

func TestDeleteEverythingKeepsTreeValid(t *testing.T) {
	tree := New(5)
	keys := rand.Perm(500)
	for _, k := range keys {
		tree.Insert(k, k)
	}
	for _, k := range keys {
		if !tree.Delete(k) {
			t.Fatalf("Delete(%d) reported missing", k)
		}
	}
	rep := Inspect(tree)
	if !rep.OK() {
		t.Fatalf("empty tree failed inspection:\n%s", rep)
	}
	if tree.Size() != 0 {
		t.Fatalf("expected empty tree, size=%d", tree.Size())
	}
}

func TestRandomizedInsertDeleteInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tree := New(6)
	live := make(map[int]bool)
	for i := 0; i < 20000; i++ {
		k := rng.Intn(500)
		if rng.Intn(2) == 0 {
			tree.Insert(k, k)
			live[k] = true
		} else {
			tree.Delete(k)
			delete(live, k)
		}
	}
	rep := Inspect(tree)
	if !rep.OK() {
		t.Fatalf("invariants broken after random ops:\n%s", rep)
	}
	if tree.Size() != len(live) {
		t.Fatalf("size=%d, want %d", tree.Size(), len(live))
	}
	for k := range live {
		if _, ok := tree.Get(k); !ok {
			t.Fatalf("live key %d missing", k)
		}
	}
}

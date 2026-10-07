package depcache

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func val(n int) []byte { return make([]byte, n) }

// checkInvariants verifies the reference-graph and byte-conservation
// model against the cache's internal state.
func checkInvariants(t *testing.T, c *Cache, modelBorrows map[string]int) {
	t.Helper()
	sizes, deps, borrows := c.Snapshot()

	// Byte conservation: accounted bytes equal the sum of live values
	// and never exceed capacity.
	var sum int64
	for _, s := range sizes {
		sum += s
	}
	if got := c.Used(); got != sum {
		t.Fatalf("byte conservation violated: Used()=%d, sum of live sizes=%d", got, sum)
	}
	if c.Used() > c.Capacity() {
		t.Fatalf("capacity exceeded: used=%d capacity=%d", c.Used(), c.Capacity())
	}

	// Every dependency edge points at a live entry.
	for k, ds := range deps {
		for _, d := range ds {
			if _, ok := sizes[d]; !ok {
				t.Fatalf("dangling edge: %q depends on missing %q", k, d)
			}
		}
	}

	// The graph is acyclic.
	visit := map[string]int{} // 0=unvisited 1=in-stack 2=done
	var dfs func(k string) bool
	dfs = func(k string) bool {
		if visit[k] == 1 {
			return false
		}
		if visit[k] == 2 {
			return true
		}
		visit[k] = 1
		for _, d := range deps[k] {
			if !dfs(d) {
				return false
			}
		}
		visit[k] = 2
		return true
	}
	for k := range deps {
		if !dfs(k) {
			t.Fatalf("dependency cycle detected at %q", k)
		}
	}

	// children is the exact inverse of deps (internal consistency).
	for k, e := range c.entries {
		for d, p := range e.deps {
			if p.children[k] != e {
				t.Fatalf("inverse edge broken: %q lists dep %q but parent lacks child backlink", k, d)
			}
		}
		for ck, ch := range e.children {
			if ch.deps[k] != e {
				t.Fatalf("inverse edge broken: %q lists child %q but child lacks dep backlink", k, ck)
			}
		}
	}

	// Borrow counts match the model, and borrowed entries are live.
	for k, n := range modelBorrows {
		if n == 0 {
			continue
		}
		if _, ok := sizes[k]; !ok {
			t.Fatalf("borrowed entry %q was evicted", k)
		}
		if borrows[k] != n {
			t.Fatalf("borrow count for %q: cache=%d model=%d", k, borrows[k], n)
		}
	}
}

func TestReplacementByteConservation(t *testing.T) {
	c := New(100)
	mustPut(t, c, "base", val(40))
	mustPut(t, c, "mid", val(20), "base")
	mustPut(t, c, "leaf", val(10), "mid")

	// Replace with a larger value: bytes are re-accounted, children keep
	// their dependency on the key.
	mustPut(t, c, "base", val(50))
	if got := c.Used(); got != 80 {
		t.Fatalf("Used after grow = %d, want 80", got)
	}
	// Replace with a smaller value.
	mustPut(t, c, "base", val(10))
	if got := c.Used(); got != 40 {
		t.Fatalf("Used after shrink = %d, want 40", got)
	}
	// Dependents survive replacement of their parent.
	if _, ok := c.Get("mid"); !ok {
		t.Fatal("mid missing after parent replacement")
	}
	if _, ok := c.Get("leaf"); !ok {
		t.Fatal("leaf missing after parent replacement")
	}
	// Replacement may redeclare dependencies.
	mustPut(t, c, "mid", val(20)) // drop dep on base
	sizes, deps, _ := c.Snapshot()
	if len(deps["mid"]) != 0 {
		t.Fatalf("mid deps = %v, want none", deps["mid"])
	}
	if sizes["base"] != 10 || sizes["mid"] != 20 || sizes["leaf"] != 10 {
		t.Fatalf("sizes = %v", sizes)
	}
	checkInvariants(t, c, nil)
}

func TestDependencyCycleRejection(t *testing.T) {
	c := New(1000)
	mustPut(t, c, "c", val(1))
	mustPut(t, c, "b", val(1), "c")
	mustPut(t, c, "a", val(1), "b")

	if err := c.Put("c", val(1), "a"); err != ErrCycle {
		t.Fatalf("c<-a: got %v, want ErrCycle", err)
	}
	if err := c.Put("c", val(1), "b"); err != ErrCycle {
		t.Fatalf("c<-b: got %v, want ErrCycle", err)
	}
	if err := c.Put("x", val(1), "x"); err != ErrCycle {
		t.Fatalf("self dep: got %v, want ErrCycle", err)
	}
	if err := c.Put("x", val(1), "ghost"); err != ErrMissingDep {
		t.Fatalf("missing dep: got %v, want ErrMissingDep", err)
	}
	// Failed puts leave the graph untouched.
	_, deps, _ := c.Snapshot()
	want := map[string][]string{"a": {"b"}, "b": {"c"}, "c": {}}
	for k, ws := range want {
		got := append([]string(nil), deps[k]...)
		sort.Strings(got)
		if fmt.Sprint(got) != fmt.Sprint(ws) {
			t.Fatalf("deps[%q] = %v, want %v", k, got, ws)
		}
	}
	if got := c.Used(); got != 3 {
		t.Fatalf("Used = %d, want 3", got)
	}
	checkInvariants(t, c, nil)
}

func TestEvictionRevokesDependentsFirst(t *testing.T) {
	c := New(100)
	mustPut(t, c, "root", val(30))
	mustPut(t, c, "child", val(20), "root")
	mustPut(t, c, "grand", val(10), "child")
	mustPut(t, c, "other", val(40))
	// Refresh root so the child subtree and "other" are colder.
	if _, ok := c.Get("root"); !ok {
		t.Fatal("root missing")
	}

	// Need 50 bytes. Coldest group is child+grand (30), then other (40);
	// root itself is pinned by recency, not by the child eviction:
	// revocation flows from parents to children, never upward.
	mustPut(t, c, "new", val(50))
	if _, ok := c.Get("root"); !ok {
		t.Fatal("root must survive eviction of its children")
	}
	for _, k := range []string{"child", "grand", "other"} {
		if _, ok := c.Get(k); ok {
			t.Fatalf("%q should have been evicted", k)
		}
	}
	if got := c.Used(); got != 80 {
		t.Fatalf("Used = %d, want 80 (root 30 + new 50)", got)
	}
	checkInvariants(t, c, nil)

	// Evicting a parent revokes its whole dependent subtree with it.
	c2 := New(100)
	mustPut(t, c2, "root", val(30))
	mustPut(t, c2, "child", val(20), "root")
	mustPut(t, c2, "grand", val(10), "child")
	mustPut(t, c2, "new", val(50)) // used=110 -> evict coldest group
	// Coldest is root; its closure (root+child+grand=60) is revoked.
	for _, k := range []string{"root", "child", "grand"} {
		if _, ok := c2.Get(k); ok {
			t.Fatalf("%q should have been revoked with its group", k)
		}
	}
	if got := c2.Used(); got != 50 {
		t.Fatalf("Used = %d, want 50 (only new remains)", got)
	}
	checkInvariants(t, c2, nil)
}

func TestBorrowBlocksGroupAndRetryAfterRelease(t *testing.T) {
	c := New(100)
	mustPut(t, c, "root", val(30))
	mustPut(t, c, "child", val(30), "root")
	mustPut(t, c, "free", val(40))

	// Borrow the child: the whole group (child + its dependency root)
	// is pinned, together 60 bytes.
	h, ok := c.Get("child")
	if !ok {
		t.Fatal("borrow child failed")
	}

	// Need 50 bytes: only "free" (40) is evictable, group is pinned.
	if err := c.Put("big", val(50)); err != ErrNoSpace {
		t.Fatalf("put while pinned: got %v, want ErrNoSpace", err)
	}
	// Rejection is clean: nothing half-stored, bytes conserved.
	if _, ok := c.Get("big"); ok {
		t.Fatal("rejected entry must not be stored")
	}
	if got := c.Used(); got != 100 {
		t.Fatalf("Used after rejection = %d, want 100", got)
	}

	// After release the group becomes evictable and the retry succeeds.
	// The root+child group (60 bytes) covers the 50 needed; "free" is
	// warmer and survives.
	h.Release()
	mustPut(t, c, "big", val(50))
	if got := c.Used(); got != 90 {
		t.Fatalf("Used after retry = %d, want 90 (free 40 + big 50)", got)
	}
	for _, k := range []string{"root", "child"} {
		if _, ok := c.Get(k); ok {
			t.Fatalf("%q should have been evicted for the retry", k)
		}
	}
	if _, ok := c.Get("free"); !ok {
		t.Fatal("free is warmer than the evicted group and must survive")
	}
	checkInvariants(t, c, nil)
}

func TestBorrowDoesNotBlockUnrelatedEntries(t *testing.T) {
	c := New(100)
	mustPut(t, c, "pinned", val(30))
	mustPut(t, c, "a", val(30))
	mustPut(t, c, "b", val(40))

	h, _ := c.Get("pinned")
	defer h.Release()

	// 70 bytes evictable (a, b); need 60.
	mustPut(t, c, "new", val(60))
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should be evicted")
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should be evicted")
	}
	if got := c.Used(); got != 90 {
		t.Fatalf("Used = %d, want 90", got)
	}
	checkInvariants(t, c, map[string]int{"pinned": 1})
}

func TestOversizedEntryRejectedWithoutEvicting(t *testing.T) {
	c := New(100)
	mustPut(t, c, "keep", val(60))
	if err := c.Put("huge", val(101)); err != ErrNoSpace {
		t.Fatalf("got %v, want ErrNoSpace", err)
	}
	if got := c.Used(); got != 60 {
		t.Fatalf("oversized put evicted entries: Used = %d, want 60", got)
	}
}

// TestModelFuzz drives random operations and checks the reference-graph
// and byte-conservation invariants after every step.
func TestModelFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	c := New(200)
	borrows := map[string]int{}
	var handles []*Handle
	keys := []string{}

	modelDeps := map[string]map[string]bool{}
	reachesInModel := func(from, to string) bool {
		seen := map[string]bool{}
		var walk func(k string) bool
		walk = func(k string) bool {
			if k == to {
				return true
			}
			if seen[k] {
				return false
			}
			seen[k] = true
			for d := range modelDeps[k] {
				if walk(d) {
					return true
				}
			}
			return false
		}
		return walk(from)
	}

	for step := 0; step < 2000; step++ {
		switch rng.Intn(4) {
		case 0, 1: // Put
			var key string
			if len(keys) > 0 && rng.Intn(3) == 0 {
				key = keys[rng.Intn(len(keys))] // replace existing
			} else {
				key = fmt.Sprintf("k%d", rng.Intn(40))
			}
			size := rng.Intn(30)
			// Pick deps that keep the model acyclic.
			var deps []string
			for _, d := range keys {
				if d == key || rng.Intn(4) != 0 {
					continue
				}
				if reachesInModel(d, key) {
					continue // would close a cycle
				}
				deps = append(deps, d)
			}
			err := c.Put(key, val(size), deps...)
			live := false
			for _, k := range keys {
				if k == key {
					live = true
				}
			}
			if err == nil {
				if !live {
					keys = append(keys, key)
				}
				nd := map[string]bool{}
				for _, d := range deps {
					nd[d] = true
				}
				modelDeps[key] = nd
			} else if err != ErrNoSpace && err != ErrMissingDep && err != ErrCycle {
				t.Fatalf("step %d: unexpected put error %v", step, err)
			}
		case 2: // Get (borrow)
			if len(keys) == 0 {
				continue
			}
			key := keys[rng.Intn(len(keys))]
			if h, ok := c.Get(key); ok {
				handles = append(handles, h)
				borrows[key]++
			}
		case 3: // Release
			if len(handles) == 0 {
				continue
			}
			i := rng.Intn(len(handles))
			handles[i].Release()
			borrows[handles[i].Key()]--
			handles = append(handles[:i], handles[i+1:]...)
		}
		// Keep the model's key set in sync with evictions: a key is
		// dropped from the model when the cache no longer holds it and
		// nothing borrows it.
		sizes, _, _ := c.Snapshot()
		kept := keys[:0]
		for _, k := range keys {
			if _, ok := sizes[k]; ok {
				kept = append(kept, k)
			} else {
				delete(modelDeps, k)
			}
		}
		keys = kept
		checkInvariants(t, c, borrows)
	}
}

func mustPut(t *testing.T, c *Cache, key string, v []byte, deps ...string) {
	t.Helper()
	if err := c.Put(key, v, deps...); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

package cache

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustSet(t *testing.T, c *Cache, key string, size int, deps ...string) {
	t.Helper()
	if err := c.Set(key, make([]byte, size), deps...); err != nil {
		t.Fatalf("Set(%q, %d, %v): %v", key, size, deps, err)
	}
}

func mustGet(t *testing.T, c *Cache, key string) *Handle {
	t.Helper()
	h, ok := c.Get(key)
	if !ok {
		t.Fatalf("Get(%q): not found", key)
	}
	return h
}

func mustAbsent(t *testing.T, c *Cache, key string) {
	t.Helper()
	if _, ok := c.Get(key); ok {
		t.Fatalf("Get(%q): expected absent", key)
	}
}

func TestLRUReplacementAndByteAccounting(t *testing.T) {
	c := New(10)
	mustSet(t, c, "a", 4)
	mustSet(t, c, "b", 4)
	mustGet(t, c, "a").Release() // touch: a is now most-recent
	mustSet(t, c, "c", 4)        // 12 > 10, evicts LRU victim b
	mustAbsent(t, c, "b")
	mustGet(t, c, "a").Release()
	mustGet(t, c, "c").Release()
	if got := c.Used(); got != 8 {
		t.Fatalf("Used() = %d, want 8", got)
	}
	if got := c.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
}

func TestEvictParentRevokesChildrenTransitively(t *testing.T) {
	c := New(12)
	mustSet(t, c, "p", 3)
	mustSet(t, c, "c1", 3, "p")
	mustSet(t, c, "c2", 3, "c1")
	mustSet(t, c, "other", 3) // lru front->back: other, c2, c1, p
	mustSet(t, c, "big", 6)   // evicting p must revoke c1 and c2 first
	mustAbsent(t, c, "p")
	mustAbsent(t, c, "c1")
	mustAbsent(t, c, "c2")
	mustGet(t, c, "other").Release()
	mustGet(t, c, "big").Release()
	if got := c.Used(); got != 9 {
		t.Fatalf("Used() = %d, want 9", got)
	}
}

func TestBorrowBlocksGroupAndRetryAfterRelease(t *testing.T) {
	c := New(10)
	mustSet(t, c, "p", 4)
	mustSet(t, c, "c", 4, "p")
	h := mustGet(t, c, "c") // borrow child: blocks the whole {p, c} group

	err := c.Set("big", make([]byte, 5))
	if err != ErrNoSpace {
		t.Fatalf("Set while borrowed: got %v, want ErrNoSpace", err)
	}
	if got := c.Used(); got != 8 {
		t.Fatalf("rejected Set mutated state: Used() = %d, want 8", got)
	}
	if got := c.Len(); got != 2 {
		t.Fatalf("rejected Set mutated state: Len() = %d, want 2", got)
	}

	h.Release()
	mustSet(t, c, "big", 5) // retry after release succeeds
	mustAbsent(t, c, "p")
	mustAbsent(t, c, "c")
	mustGet(t, c, "big").Release()
	if got := c.Used(); got != 5 {
		t.Fatalf("Used() = %d, want 5", got)
	}
}

func TestBorrowedParentBlocksGroupEviction(t *testing.T) {
	c := New(8)
	mustSet(t, c, "p", 4)
	mustSet(t, c, "c", 4, "p")
	h := mustGet(t, c, "p")
	mustGet(t, c, "c").Release() // touch c so p becomes the LRU victim
	// Evicting c alone frees only 4 bytes; the 5-byte entry needs p's group,
	// which is blocked by the borrow.
	if err := c.Set("x", make([]byte, 5)); err != ErrNoSpace {
		t.Fatalf("got %v, want ErrNoSpace", err)
	}
	h.Release()
	mustSet(t, c, "x", 5) // retry evicts p and, with it, c
	mustAbsent(t, c, "p")
	mustAbsent(t, c, "c")
	mustGet(t, c, "x").Release()
}

func TestChildEvictsIndependentlyOfBorrowedParent(t *testing.T) {
	c := New(8)
	mustSet(t, c, "p", 4)
	mustSet(t, c, "c", 4, "p")
	h := mustGet(t, c, "p") // lru front->back: p, c; c is the victim
	mustSet(t, c, "x", 4)   // evicts c alone; the borrowed parent stays
	mustAbsent(t, c, "c")
	if got := len(h.Value()); got != 4 {
		t.Fatalf("handle value len = %d, want 4", got)
	}
	h.Release()
	mustGet(t, c, "p").Release()
	mustGet(t, c, "x").Release()
}

func TestCycleRejection(t *testing.T) {
	c := New(64)
	mustSet(t, c, "a", 1)
	mustSet(t, c, "b", 1, "a")
	mustSet(t, c, "d", 1, "b") // d -> b -> a

	cases := []struct {
		key  string
		deps []string
	}{
		{"a", []string{"a"}}, // self
		{"x", []string{"x"}}, // self, new key
		{"a", []string{"b"}}, // direct: b depends on a
		{"a", []string{"d"}}, // transitive: d -> b -> a
		{"b", []string{"d"}}, // transitive: d -> b
		{"a", []string{"d", "b"}},
	}
	for _, tc := range cases {
		if err := c.Set(tc.key, make([]byte, 1), tc.deps...); err != ErrCycle {
			t.Fatalf("Set(%q, %v): got %v, want ErrCycle", tc.key, tc.deps, err)
		}
	}
	if got := c.Used(); got != 3 {
		t.Fatalf("rejected Sets mutated state: Used() = %d, want 3", got)
	}
}

func TestMissingDependency(t *testing.T) {
	c := New(8)
	if err := c.Set("x", make([]byte, 1), "nope"); err != ErrMissingDep {
		t.Fatalf("got %v, want ErrMissingDep", err)
	}
	mustSet(t, c, "p", 1)
	if err := c.Set("x", make([]byte, 1), "p", "nope"); err != ErrMissingDep {
		t.Fatalf("got %v, want ErrMissingDep", err)
	}
	if got := c.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
}

func TestEntryTooLarge(t *testing.T) {
	c := New(4)
	if err := c.Set("x", make([]byte, 5)); err != ErrTooLarge {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	mustSet(t, c, "x", 4)
	if got := c.Used(); got != 4 {
		t.Fatalf("Used() = %d, want 4", got)
	}
}

func TestReplaceAdjustsBytesAndKeepsChildren(t *testing.T) {
	c := New(10)
	mustSet(t, c, "p", 2)
	mustSet(t, c, "c", 2, "p")
	mustSet(t, c, "p", 5) // grow: child stays attached
	mustGet(t, c, "c").Release()
	if got := c.Used(); got != 7 {
		t.Fatalf("Used() = %d, want 7", got)
	}
	mustSet(t, c, "p", 1) // shrink
	if got := c.Used(); got != 3 {
		t.Fatalf("Used() = %d, want 3", got)
	}
	// Evicting the replaced parent still revokes the child.
	mustSet(t, c, "filler", 7) // used 10
	mustSet(t, c, "n", 3)      // evicts p (LRU) and with it c
	mustAbsent(t, c, "p")
	mustAbsent(t, c, "c")
}

func TestReplaceCanChangeDeps(t *testing.T) {
	c := New(16)
	mustSet(t, c, "q", 1)
	mustSet(t, c, "p", 1)
	mustSet(t, c, "p", 1, "q") // p now depends on q
	if err := c.Set("q", make([]byte, 1), "p"); err != ErrCycle {
		t.Fatalf("got %v, want ErrCycle", err)
	}
	mustSet(t, c, "p", 1) // drop the dependency again
	mustSet(t, c, "q", 1, "p")
}

func TestReplaceBorrowedEntry(t *testing.T) {
	c := New(10)
	mustSet(t, c, "p", 4)
	h := mustGet(t, c, "p")
	mustSet(t, c, "p", 6) // replace while old version is borrowed
	if got := len(h.Value()); got != 4 {
		t.Fatalf("handle value len = %d, want 4 (old version)", got)
	}
	h.Release()
	if got := c.entries["p"].borrows; got != 0 {
		t.Fatalf("new entry borrows = %d, want 0", got)
	}
	// The released borrow must not shield the replacement from eviction.
	mustSet(t, c, "q", 6) // 12 > 10 evicts p
	mustAbsent(t, c, "p")
	mustGet(t, c, "q").Release()
}

func TestDepsProtectedDuringEviction(t *testing.T) {
	c := New(8)
	mustSet(t, c, "a", 4)
	mustSet(t, c, "b", 4)
	mustSet(t, c, "c", 4, "a") // a is LRU but protected as a dep; b is evicted
	mustGet(t, c, "a").Release()
	mustGet(t, c, "c").Release()
	mustAbsent(t, c, "b")
	if got := c.Used(); got != 8 {
		t.Fatalf("Used() = %d, want 8", got)
	}
}

func TestHandleReleaseIdempotent(t *testing.T) {
	c := New(4)
	mustSet(t, c, "a", 1)
	h := mustGet(t, c, "a")
	h.Release()
	h.Release()
	if got := c.entries["a"].borrows; got != 0 {
		t.Fatalf("borrows = %d, want 0", got)
	}
}

// checkInvariants verifies the byte-conservation model and the reference
// graph against the cache's internal state. live maps each entry with
// outstanding handles to its expected borrow count; recency lists keys from
// most to least recently touched.
func checkInvariants(t *testing.T, c *Cache, capacity int64, live map[*entry]int, recency []string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	// Byte conservation: accounted bytes equal the sum of live entry sizes
	// and never exceed capacity.
	var sum int64
	for _, e := range c.entries {
		sum += e.size
	}
	if sum != c.used {
		t.Fatalf("byte conservation violated: sum of sizes %d != used %d", sum, c.used)
	}
	if c.used < 0 || c.used > capacity {
		t.Fatalf("used %d outside [0, %d]", c.used, capacity)
	}

	// LRU list mirrors the entry set and matches the recency model.
	if c.lru.Len() != len(c.entries) {
		t.Fatalf("lru len %d != entries %d", c.lru.Len(), len(c.entries))
	}
	var order []string
	for el := c.lru.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		if c.entries[e.key] != e {
			t.Fatalf("stale lru element for %q", e.key)
		}
		if e.elem != el {
			t.Fatalf("entry %q elem pointer mismatch", e.key)
		}
		order = append(order, e.key)
	}
	var want []string
	for _, k := range recency {
		if _, ok := c.entries[k]; ok {
			want = append(want, k)
		}
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("lru order %v != recency model %v", order, want)
	}

	// Reference graph: symmetric edges, no dangling deps, borrow counts.
	for _, e := range c.entries {
		if e.borrows != live[e] {
			t.Fatalf("entry %q borrows %d != model %d", e.key, e.borrows, live[e])
		}
		for d := range e.deps {
			p := c.entries[d]
			if p == nil {
				t.Fatalf("entry %q has dangling dep %q", e.key, d)
			}
			if _, ok := p.children[e.key]; !ok {
				t.Fatalf("asymmetric edge: %q deps on %q but not a child", e.key, d)
			}
		}
		for ch := range e.children {
			ce := c.entries[ch]
			if ce == nil {
				t.Fatalf("entry %q has dangling child %q", e.key, ch)
			}
			if _, ok := ce.deps[e.key]; !ok {
				t.Fatalf("asymmetric edge: %q child of %q but no dep", ch, e.key)
			}
		}
	}

	// The dependency graph must be acyclic.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(c.entries))
	var visit func(k string) bool
	visit = func(k string) bool {
		color[k] = gray
		for d := range c.entries[k].deps {
			switch color[d] {
			case gray:
				return false
			case white:
				if !visit(d) {
					return false
				}
			}
		}
		color[k] = black
		return true
	}
	for k := range c.entries {
		if color[k] == white && !visit(k) {
			t.Fatalf("dependency cycle detected at %q", k)
		}
	}
}

func TestModelRandom(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for trial := 0; trial < 200; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		capacity := int64(rng.Intn(40))
		c := New(capacity)
		live := make(map[*entry]int)
		var handles []*Handle
		var recency []string
		touch := func(k string) {
			for i, x := range recency {
				if x == k {
					recency = append(recency[:i], recency[i+1:]...)
					break
				}
			}
			recency = append([]string{k}, recency...)
		}
		for step := 0; step < 500; step++ {
			switch op := rng.Intn(10); {
			case op < 5: // Set with random size and random (possibly cyclic) deps
				k := keys[rng.Intn(len(keys))]
				var deps []string
				for _, d := range keys {
					if rng.Intn(6) == 0 {
						deps = append(deps, d)
					}
				}
				usedBefore, lenBefore := c.Used(), c.Len()
				err := c.Set(k, make([]byte, rng.Intn(12)), deps...)
				if err != nil {
					if c.Used() != usedBefore || c.Len() != lenBefore {
						t.Fatalf("trial %d step %d: failed Set(%q) mutated state: %v", trial, step, k, err)
					}
				} else {
					touch(k)
				}
			case op < 8: // Get, sometimes holding the handle
				k := keys[rng.Intn(len(keys))]
				if h, ok := c.Get(k); ok {
					touch(k)
					live[h.e]++
					if rng.Intn(3) == 0 {
						handles = append(handles, h)
					} else {
						h.Release()
						live[h.e]--
					}
				}
			default: // Release a held handle
				if len(handles) > 0 {
					i := rng.Intn(len(handles))
					handles[i].Release()
					live[handles[i].e]--
					handles = append(handles[:i], handles[i+1:]...)
				}
			}
			checkInvariants(t, c, capacity, live, recency)
		}
	}
}

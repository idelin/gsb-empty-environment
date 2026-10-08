package arc

import (
	"math/rand"
	"testing"
)

// checkState verifies the invariants that must hold after every step:
// the target stays within [0, capacity] and every queue respects its
// size bound. Capacity is accounted in entries.
func checkState(t *testing.T, c *Cache) {
	t.Helper()
	if c.p < 0 || c.p > c.capacity {
		t.Fatalf("target p=%d outside [0, %d]", c.p, c.capacity)
	}
	if c.Len() > c.capacity {
		t.Fatalf("Len()=%d exceeds capacity %d", c.Len(), c.capacity)
	}
	if c.t1.Len()+c.b1.Len() > c.capacity {
		t.Fatalf("|T1|+|B1|=%d exceeds capacity %d", c.t1.Len()+c.b1.Len(), c.capacity)
	}
	if total := c.t1.Len() + c.t2.Len() + c.b1.Len() + c.b2.Len(); total > 2*c.capacity {
		t.Fatalf("total queues hold %d keys, more than 2*capacity=%d", total, 2*c.capacity)
	}
}

func TestCapacityOne(t *testing.T) {
	c := New(1)
	steps := []struct {
		op   func()
		want string
	}{
		{func() { c.Set("a", 1) }, "p=0 T1=[a] T2=[] B1=[] B2=[]"},
		// B1 is empty, so the evicted key leaves no ghost behind.
		{func() { c.Set("b", 2) }, "p=0 T1=[b] T2=[] B1=[] B2=[]"},
		// Re-storing a live key promotes it to the repeat-access queue.
		{func() { c.Set("b", 3) }, "p=0 T1=[] T2=[b] B1=[] B2=[]"},
		// Miss on a full cache: T2's LRU becomes a valueless ghost.
		{func() { c.Set("c", 4) }, "p=0 T1=[c] T2=[] B1=[] B2=[b]"},
		// Ghost hit on b: T1's entry is evicted to B1, b returns to T2.
		{func() { c.Set("b", 5) }, "p=0 T1=[] T2=[b] B1=[c] B2=[]"},
	}
	for i, s := range steps {
		s.op()
		t.Logf("step %d: %s", i, c.Snapshot())
		if got := c.Snapshot(); got != s.want {
			t.Errorf("step %d: got  %s\nwant %s", i, got, s.want)
		}
		checkState(t, c)
	}
	if v, ok := c.Get("b"); !ok || v != 5 {
		t.Errorf("Get(b) = %v, %v; want 5, true", v, ok)
	}
	if v, ok := c.Get("c"); ok || v != nil {
		t.Errorf("Get(c) on ghost = %v, %v; want nil, false", v, ok)
	}
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) after full eviction should miss")
	}
	checkState(t, c)
}

func TestRepeatedDelete(t *testing.T) {
	c := New(2)
	c.Set("a", 1)
	c.Set("b", 2)
	if !c.Delete("a") {
		t.Error("first Delete(a) should report a live entry")
	}
	if c.Delete("a") {
		t.Error("second Delete(a) should be a no-op reporting false")
	}
	if c.Delete("missing") {
		t.Error("Delete(missing) should report false")
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}
	checkState(t, c)

	// Evict c into ghost list B1, then delete the ghost record.
	c.Get("b")
	c.Set("c", 3)
	c.Set("d", 4)
	t.Logf("before ghost delete: %s", c.Snapshot())
	if c.Delete("c") {
		t.Error("Delete(c) on a ghost record should report false")
	}
	if c.Delete("c") {
		t.Error("repeated Delete(c) should stay a no-op")
	}
	checkState(t, c)
	if got, want := c.Snapshot(), "p=0 T1=[d] T2=[b] B1=[] B2=[]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if c.Delete("b") != true || c.Delete("d") != true {
		t.Error("deleting live entries should report true")
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
	checkState(t, c)
}

func TestTargetAdjustment(t *testing.T) {
	c := New(2)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Get("a")
	c.Get("b")
	c.Set("c", 3) // evicts a to B2
	c.Set("d", 4) // evicts c to B1
	if got := c.Target(); got != 0 {
		t.Fatalf("p = %d, want 0 before any ghost hit", got)
	}
	t.Logf("before ghost hits: %s", c.Snapshot())

	c.Set("c", 5) // B1 hit: p grows by max(|B2|/|B1|, 1) = 1
	if got := c.Target(); got != 1 {
		t.Errorf("after B1 hit p = %d, want 1", got)
	}
	t.Logf("after B1 hit:      %s", c.Snapshot())

	c.Set("b", 6) // B2 hit: p shrinks by max(|B1|/|B2|, 1) = 1
	if got := c.Target(); got != 0 {
		t.Errorf("after B2 hit p = %d, want 0", got)
	}
	t.Logf("after B2 hit:      %s", c.Snapshot())
	if got, want := c.Snapshot(), "p=0 T1=[] T2=[b c] B1=[d] B2=[a]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	checkState(t, c)
}

// TestDifferentialNaive replays identical random operation sequences
// against the cache and the naive slice-based reference model, requiring
// identical observable behaviour and queue states at every step.
func TestDifferentialNaive(t *testing.T) {
	for _, capacity := range []int{1, 2, 3, 5} {
		rng := rand.New(rand.NewSource(int64(capacity)*7919 + 13))
		real := New(capacity)
		model := newNaive(capacity)
		minP, maxP := capacity, 0
		keys := make([]string, 0, 2*capacity+3)
		for i := 0; i < 2*capacity+3; i++ {
			keys = append(keys, string(rune('a'+i)))
		}
		for i := 0; i < 4000; i++ {
			key := keys[rng.Intn(len(keys))]
			switch rng.Intn(10) {
			case 0, 1:
				if got, want := real.Delete(key), model.delete(key); got != want {
					t.Fatalf("c=%d step %d: Delete(%s) = %v, model %v", capacity, i, key, got, want)
				}
			case 2, 3, 4:
				gotV, gotOK := real.Get(key)
				wantV, wantOK := model.get(key)
				if gotOK != wantOK || gotV != wantV {
					t.Fatalf("c=%d step %d: Get(%s) = %v,%v; model %v,%v",
						capacity, i, key, gotV, gotOK, wantV, wantOK)
				}
			default:
				real.Set(key, i)
				model.set(key, i)
			}
			if got, want := real.Snapshot(), model.snapshot(); got != want {
				t.Fatalf("c=%d step %d op on %s:\ngot   %s\nmodel %s", capacity, i, key, got, want)
			}
			if real.Len() != model.len() {
				t.Fatalf("c=%d step %d: Len() = %d, model %d", capacity, i, real.Len(), model.len())
			}
			checkState(t, real)
			if real.p < minP {
				minP = real.p
			}
			if real.p > maxP {
				maxP = real.p
			}
		}
		t.Logf("capacity %d: p ranged over [%d, %d], final state %s", capacity, minP, maxP, real.Snapshot())
		if capacity > 1 && (minP != 0 || maxP != capacity) {
			t.Errorf("capacity %d: target p ranged over [%d, %d], want both bounds 0 and %d exercised",
				capacity, minP, maxP, capacity)
		}
	}
}

// TestEntryAccounting pins down that capacity is charged per entry,
// never per value size.
func TestEntryAccounting(t *testing.T) {
	c := New(2)
	c.Set("small", 1)
	c.Set("large", make([]byte, 1<<20))
	if c.Len() != 2 {
		t.Errorf("Len() = %d, want 2 entries regardless of value size", c.Len())
	}
}

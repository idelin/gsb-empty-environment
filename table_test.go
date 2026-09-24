package freqsketch

import (
	"fmt"
	"sync"
	"testing"
)

func newTestTable(budget int) (*Sketch, *Table) {
	sk := NewSketch(SketchConfig{})
	return sk, NewTable(sk, budget)
}

func TestAdmitWithinBudget(t *testing.T) {
	sk, tbl := newTestTable(100)
	sk.Add("a", 1)
	if !tbl.Admit("a", 60) {
		t.Fatal("first admit should succeed")
	}
	if tbl.Used() != 60 {
		t.Fatalf("Used = %d, want 60", tbl.Used())
	}
	// Re-admitting an existing key is a no-op refresh.
	if !tbl.Admit("a", 90) {
		t.Fatal("re-admit of resident key should succeed")
	}
	if tbl.Used() != 60 {
		t.Fatalf("Used after refresh = %d, want 60 (unchanged)", tbl.Used())
	}
	// Oversized item can never be admitted.
	if tbl.Admit("huge", 101) {
		t.Fatal("item larger than budget must be rejected")
	}
}

// TestAdmissionComparesWithEvicted: a candidate is admitted only when
// its estimate beats the entry it would evict.
func TestAdmissionComparesWithEvicted(t *testing.T) {
	sk, tbl := newTestTable(100)
	for i := 0; i < 10; i++ {
		sk.Add("hot", 1)
	}
	sk.Add("cold", 1)
	for i := 0; i < 5; i++ {
		sk.Add("warm", 1)
	}
	sk.Add("loser", 1)

	if !tbl.Admit("cold", 60) {
		t.Fatal("cold admitted into empty table")
	}
	// warm (5) beats cold (1): evict cold, admit warm.
	if !tbl.Admit("warm", 60) {
		t.Fatal("warm should evict colder entry")
	}
	if _, ok := tbl.Get("cold"); ok {
		t.Fatal("cold should have been evicted")
	}
	if tbl.Used() != 60 {
		t.Fatalf("Used = %d, want 60", tbl.Used())
	}
	// loser (1) cannot evict warm (5): rejected, table untouched.
	if tbl.Admit("loser", 60) {
		t.Fatal("loser must not evict a hotter entry")
	}
	if _, ok := tbl.Get("warm"); !ok {
		t.Fatal("warm must survive rejected admission (atomicity)")
	}
	if tbl.Used() != 60 || tbl.Len() != 1 {
		t.Fatalf("table mutated by rejected admission: used=%d len=%d", tbl.Used(), tbl.Len())
	}
	// hot (10) evicts warm (5).
	if !tbl.Admit("hot", 60) {
		t.Fatal("hot should evict warm")
	}
	if _, ok := tbl.Get("warm"); ok {
		t.Fatal("warm should have been evicted by hot")
	}
}

// TestMultiVictimEviction: a large candidate may evict several colder
// entries, all within one atomic decision.
func TestMultiVictimEviction(t *testing.T) {
	sk, tbl := newTestTable(100)
	for _, k := range []string{"x", "y", "z"} {
		sk.Add(k, 1)
		if !tbl.Admit(k, 30) {
			t.Fatalf("admit %s failed", k)
		}
	}
	for i := 0; i < 9; i++ {
		sk.Add("big", 1)
	}
	if !tbl.Admit("big", 90) {
		t.Fatal("big should evict x,y,z")
	}
	if tbl.Len() != 1 || tbl.Used() != 90 {
		t.Fatalf("used=%d len=%d, want 90/1", tbl.Used(), tbl.Len())
	}
}

// TestEvictionTieBreakDeterministic: equal-frequency ties evict the
// lexicographically smallest key, so decisions are reproducible.
func TestEvictionTieBreakDeterministic(t *testing.T) {
	sk, tbl := newTestTable(60)
	sk.Add("aa", 2)
	sk.Add("bb", 2)
	tbl.Admit("aa", 30)
	tbl.Admit("bb", 30)
	sk.Add("cc", 2)
	if !tbl.Admit("cc", 30) {
		t.Fatal("cc ties with occupants and should win the tie")
	}
	if _, ok := tbl.Get("aa"); ok {
		t.Fatal("tie-break must evict aa (smallest key)")
	}
	if _, ok := tbl.Get("bb"); !ok {
		t.Fatal("bb must survive the tie")
	}
}

// TestByteBudgetInvariantConcurrent: concurrent admissions never exceed
// the budget and never drive usage negative (run with -race).
func TestByteBudgetInvariantConcurrent(t *testing.T) {
	sk, tbl := newTestTable(256)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				key := fmt.Sprintf("k%d-%d", id, i%40)
				sk.Add(key, 1)
				tbl.Admit(key, 1+((id+i)%16))
				if u := tbl.Used(); u < 0 || u > 256 {
					t.Errorf("budget invariant violated: used=%d", u)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	// Final consistency: used equals the sum of entry sizes.
	sum := 0
	for _, e := range tbl.Entries() {
		sum += e.Bytes
	}
	if sum != tbl.Used() {
		t.Fatalf("used=%d but entries sum to %d", tbl.Used(), sum)
	}
}

// TestTableSnapshotRestore: checkpoint/restore reproduces decisions.
func TestTableSnapshotRestore(t *testing.T) {
	sk, tbl := newTestTable(60)
	sk.Add("a", 5)
	sk.Add("b", 3)
	tbl.Admit("a", 30)
	snap := tbl.Snapshot()
	tbl.Admit("b", 30)
	tbl.Restore(snap)
	if tbl.Len() != 1 || tbl.Used() != 30 {
		t.Fatalf("after restore len=%d used=%d, want 1/30", tbl.Len(), tbl.Used())
	}
	if _, ok := tbl.Get("a"); !ok {
		t.Fatal("a missing after restore")
	}
}

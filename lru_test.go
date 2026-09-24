package paircache

import (
	"fmt"
	"testing"
)

// Baseline sanity: miss -> hit, LRU eviction order, byte accounting.
func TestLRUBasic(t *testing.T) {
	c := NewLRU(Options{CapacityBytes: 128, Loader: fixedLoader(64)})
	l, err := c.Acquire("a")
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	l, err = c.Acquire("b")
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	l, err = c.Acquire("a") // hit, a now MRU
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	if s := c.Stats(); s.Hits != 1 || s.Misses != 2 {
		t.Fatalf("Hits=%d Misses=%d, want 1/2", s.Hits, s.Misses)
	}
	l, err = c.Acquire("c") // evicts b (LRU), not a
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	if _, ok := c.ix["b"]; ok {
		t.Fatal("b should be evicted (LRU)")
	}
	if _, ok := c.ix["a"]; !ok {
		t.Fatal("a should still be resident")
	}
	if got := c.Stats().ResidentBytes; got != 128 {
		t.Fatalf("ResidentBytes = %d, want 128", got)
	}
}

func TestLRULeasePinsEvictedMemory(t *testing.T) {
	var freed []string
	c := NewLRU(Options{
		CapacityBytes: 64,
		Loader:        fixedLoader(64),
		OnFree:        func(k string, _ int64) { freed = append(freed, k) },
	})
	lease, err := c.Acquire("a")
	if err != nil {
		t.Fatal(err)
	}
	l, err := c.Acquire("b") // evicts a, but a is leased
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	if len(freed) != 0 {
		t.Fatalf("leased entry freed early: %v", freed)
	}
	if got := c.Stats().PhysicalBytes; got != 128 {
		t.Fatalf("PhysicalBytes = %d, want 128", got)
	}
	lease.Release()
	if len(freed) != 1 || freed[0] != "a" {
		t.Fatalf("freed = %v, want [a]", freed)
	}
	if got := c.Stats().PhysicalBytes; got != 64 {
		t.Fatalf("PhysicalBytes = %d, want 64", got)
	}
}

// Variable-length entries: capacity is counted in bytes, not item count.
func TestLRUVariableSizeEntries(t *testing.T) {
	c := NewLRU(Options{CapacityBytes: 100, Loader: func(k string) ([]byte, error) {
		sizes := map[string]int{"small": 10, "mid": 40, "big": 60}
		return make([]byte, sizes[k]), nil
	}})
	for _, k := range []string{"small", "mid", "big"} {
		l, err := c.Acquire(k)
		if err != nil {
			t.Fatal(err)
		}
		l.Release()
	}
	// 10+40+60 = 110 > 100: "small" must have been evicted.
	if _, ok := c.ix["small"]; ok {
		t.Fatal("small should be evicted to fit byte capacity")
	}
	if got := c.Stats().ResidentBytes; got != 100 {
		t.Fatalf("ResidentBytes = %d, want 100", got)
	}
}

func TestLRUDelete(t *testing.T) {
	c := NewLRU(Options{CapacityBytes: 256, Loader: fixedLoader(64)})
	for i := 0; i < 3; i++ {
		l, _ := c.Acquire(fmt.Sprintf("k%d", i))
		l.Release()
	}
	c.Delete("k1")
	if _, ok := c.ix["k1"]; ok {
		t.Fatal("k1 should be deleted")
	}
	if got := c.Stats().ResidentBytes; got != 128 {
		t.Fatalf("ResidentBytes = %d, want 128", got)
	}
	if got := c.Stats().PhysicalBytes; got != 128 {
		t.Fatalf("PhysicalBytes = %d, want 128", got)
	}
}

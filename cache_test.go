package decayfreq

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// TestAdmissionPrefersFrequentItems: a hot candidate evicts cold victims,
// while a cold candidate cannot evict a hot resident.
func TestAdmissionPrefersFrequentItems(t *testing.T) {
	sk := NewSketch(4, 1024)
	c := NewCache(sk, 100)
	for i := 0; i < 100; i++ {
		sk.Add("hot", 1)
	}
	sk.Add("cold1", 1)
	sk.Add("cold2", 1)
	if !c.Admit("cold1", 60) || !c.Admit("cold2", 40) {
		t.Fatal("cold items should fill the empty cache")
	}
	if !c.Admit("hot", 100) {
		t.Fatal("hot candidate should evict cold victims")
	}
	if c.Contains("cold1") || c.Contains("cold2") {
		t.Fatal("cold victims should have been evicted")
	}
	// A cold candidate must not displace the hot resident.
	sk.Add("lukewarm", 2)
	if c.Admit("lukewarm", 50) {
		t.Fatal("cold candidate should not evict the hot resident")
	}
	if !c.Contains("hot") {
		t.Fatal("hot resident must be retained")
	}
}

// TestAdmissionAtomicity: a rejected candidate leaves cache state untouched,
// and the byte quota is never exceeded.
func TestAdmissionAtomicity(t *testing.T) {
	sk := NewSketch(4, 256)
	c := NewCache(sk, 128)
	rng := rand.New(rand.NewSource(7))
	for step := 0; step < 20000; step++ {
		key := itoa(rng.Intn(64))
		size := 1 + rng.Intn(200)
		sk.Add(key, 1)
		before := c.Snapshot()
		usedBefore := c.Used()
		admitted := c.Admit(key, size)
		used := c.Used()
		if used > 128 {
			t.Fatalf("step %d: used %d exceeds quota 128", step, used)
		}
		// Quota accounting must equal the sum of entry sizes.
		sum := 0
		for _, e := range c.Snapshot() {
			sum += e.Size
		}
		if sum != used {
			t.Fatalf("step %d: used %d != sum of sizes %d", step, used, sum)
		}
		wasPresent := false
		for _, e := range before {
			if e.Key == key {
				wasPresent = true
			}
		}
		if !admitted && !wasPresent {
			// Rejection must be atomic: nothing changed.
			if !reflect.DeepEqual(before, c.Snapshot()) || used != usedBefore {
				t.Fatalf("step %d: rejected admit mutated state", step)
			}
		}
	}
}

// TestOversizeItemRejected: an item larger than the whole quota never enters.
func TestOversizeItemRejected(t *testing.T) {
	sk := NewSketch(4, 64)
	c := NewCache(sk, 10)
	sk.Add("big", 1000)
	if c.Admit("big", 11) {
		t.Fatal("oversize item must be rejected")
	}
	if c.Used() != 0 || c.Len() != 0 {
		t.Fatal("rejected oversize item mutated state")
	}
}

// TestConcurrentAdmit hammers Admit from goroutines; quota invariants are
// checked afterwards. Run with -race.
func TestConcurrentAdmit(t *testing.T) {
	sk := NewSketch(4, 256)
	c := NewCache(sk, 256)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 3000; i++ {
				k := itoa(rng.Intn(100))
				sk.Add(k, 1)
				c.Admit(k, 1+rng.Intn(64))
			}
		}(g)
	}
	wg.Wait()
	if used := c.Used(); used > 256 {
		t.Fatalf("used %d exceeds quota", used)
	}
	sum := 0
	for _, e := range c.Snapshot() {
		sum += e.Size
	}
	if sum != c.Used() {
		t.Fatalf("used %d != sum of sizes %d", c.Used(), sum)
	}
}

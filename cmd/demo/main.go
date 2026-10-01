// Command demo walks through the sparse array semantics on a small array,
// then shows physical occupancy and update cost at a huge logical length.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"sparsearray/sparse"
)

func small() {
	fmt.Println("=== small walkthrough (n=16, default 0) ===")
	a := sparse.New(16)
	a.Set(3, 10)
	a.Set(5, 0) // explicit zero: a real exception, not "missing"
	fmt.Printf("Set(3,10), Set(5,0): Get(3)=%d Get(4)=%d Get(5)=%d IsException(5)=%v IsException(4)=%v\n",
		a.Get(3), a.Get(4), a.Get(5), a.IsException(5), a.IsException(4))

	a.RangeAdd(2, 6, 4) // lazy layer, no materialization
	fmt.Printf("RangeAdd(2,6,+4): Get(3)=%d Get(5)=%d RangeSum(0,15)=%d\n",
		a.Get(3), a.Get(5), a.RangeSum(0, 15))

	snap := a.Snapshot()
	a.SetDefault(2, false) // unassigned positions follow; explicit values keep identity
	fmt.Printf("SetDefault(2, followOld=false): Get(3)=%d Get(4)=%d Get(5)=%d (explicit 0 survives)\n",
		a.Get(3), a.Get(4), a.Get(5))
	fmt.Printf("snapshot from before: Get(3)=%d Get(4)=%d Default()=%d (frozen)\n",
		snap.Get(3), snap.Get(4), snap.Default())

	// Non-negative cumulative locate.
	b := sparse.New(10)
	for p, v := range map[uint64]int64{1: 2, 3: 3, 4: 1, 7: 4} {
		b.Set(p, v)
	}
	fmt.Printf("locate: values 0 2 0 3 1 0 0 4 0 0, total=%d\n", b.Total())
	for _, k := range []int64{1, 3, 6, 10} {
		p, ok := b.Locate(k)
		fmt.Printf("  Locate(%d) = (%d, %v)\n", k, p, ok)
	}
	b.Set(2, -1)
	_, ok := b.Locate(1)
	fmt.Printf("  after Set(2,-1): Locate(1) ok=%v (closed for non-non-negative arrays)\n", ok)
	fmt.Println()
}

func large() {
	fmt.Println("=== large logical length evidence (n = 2^40) ===")
	n := uint64(1) << 40
	a := sparse.New(n)
	a.SetDefault(2, false)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 10000; i++ {
		a.Set(uint64(rng.Int63n(int64(n))), int64(rng.Intn(100)))
	}
	for i := 0; i < 100; i++ {
		lo := uint64(rng.Int63n(int64(n / 2)))
		a.RangeAdd(lo, lo+n/3, int64(rng.Intn(10)))
	}
	st := a.Stats()
	fmt.Printf("logical length        : %d (2^40)\n", st.LogicalLen)
	fmt.Printf("dense []int64 would be: %d bytes (%.1f TiB)\n", st.DenseBytes, float64(st.DenseBytes)/(1<<40))
	fmt.Printf("exceptions / layers   : %d / %d\n", st.Exceptions, st.Layers)
	fmt.Printf("physical encoding     : %d bytes (%.2f bytes/exception)\n",
		st.PhysicalBytes, float64(st.PhysicalBytes)/float64(st.Exceptions))
	fmt.Printf("in-memory estimate    : %d bytes\n", st.InMemoryBytes)

	timeOp := func(name string, iters int, f func()) {
		start := time.Now()
		for i := 0; i < iters; i++ {
			f()
		}
		fmt.Printf("%-22s: %v/op over %d ops\n", name, time.Since(start)/time.Duration(iters), iters)
	}
	timeOp("RangeSum (1/4 span)", 20000, func() {
		lo := uint64(rng.Int63n(int64(n / 2)))
		a.RangeSum(lo, lo+n/4)
	})
	// Locate runs before the RangeAdd timing loop on purpose: lazy layers
	// accumulate until the next SetDefault compaction, and query cost is
	// proportional to the pending layer count.
	total := a.Total()
	timeOp("Locate (cumulative)", 50, func() {
		a.Locate(rng.Int63n(total) + 1)
	})
	timeOp("RangeAdd (lazy)", 20000, func() {
		lo := uint64(rng.Int63n(int64(n / 2)))
		a.RangeAdd(lo, lo+n/4, 1)
	})
	timeOp("Set (point update)", 20000, func() {
		a.Set(uint64(rng.Int63n(int64(n))), 1)
	})
	st = a.Stats()
	fmt.Printf("after timing loop     : exceptions=%d layers=%d physical=%d bytes\n",
		st.Exceptions, st.Layers, st.PhysicalBytes)
}

func main() {
	small()
	large()
}

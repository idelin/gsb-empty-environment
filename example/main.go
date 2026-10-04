// Command example demonstrates the compressed bitmap: sparse and dense
// inserts, automatic container selection, set operations, and parallel
// evaluation fed by the lock-free queue.
package main

import (
	"fmt"
	"math/rand"
	"time"

	roaring "github.com/example/roaring"
)

func main() {
	// --- Insert sparse, discrete IDs -------------------------------------
	sparse := roaring.New()
	rng := rand.New(rand.NewSource(42))
	xs := make([]uint32, 200000)
	for i := range xs {
		xs[i] = rng.Uint32()
	}
	sparse.AddMany(xs)

	// --- Insert dense, contiguous ID ranges -------------------------------
	dense := roaring.New()
	dense.AddRange(10000000, 19999999) // 10M contiguous IDs
	dense.AddRange(30000000, 30000000+5000000)
	dense.Add(7)
	dense.Add(1 << 31)

	fmt.Printf("sparse: cardinality=%d stats=%+v\n", sparse.Cardinality(), sparse.Stats())
	fmt.Printf("dense : cardinality=%d stats=%+v\n", dense.Cardinality(), dense.Stats())

	// --- Microsecond-scale set operations ---------------------------------
	a := roaring.New()
	b := roaring.New()
	a.AddRange(0, 67108863)         // 1024 dense containers (~67M IDs)
	b.AddRange(33554432, 100663295) // overlapping 1024 dense containers

	start := time.Now()
	and := a.And(b)
	fmt.Printf("AND  (%d results) took %v\n", and.Cardinality(), time.Since(start))

	start = time.Now()
	or := a.Or(b)
	fmt.Printf("OR   (%d results) took %v\n", or.Cardinality(), time.Since(start))

	start = time.Now()
	xor := a.Xor(b)
	fmt.Printf("XOR  (%d results) took %v\n", xor.Cardinality(), time.Since(start))

	start = time.Now()
	pand := a.ParallelAnd(b, 8)
	fmt.Printf("ParallelAND(8 workers) took %v, matches=%v\n",
		time.Since(start), pand.Cardinality() == and.Cardinality())

	// --- Lock-free MPMC queue ----------------------------------------------
	q := roaring.NewLFQueue(1024)
	for i := 0; i < 100; i++ {
		q.Push(i)
	}
	sum := 0
	for {
		v, ok := q.Pop()
		if !ok {
			break
		}
		sum += v.(int)
	}
	fmt.Printf("queue: drained 100 items, sum=%d (want 4950)\n", sum)
}

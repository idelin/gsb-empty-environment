// Demo: sparse + dense inserts, automatic container selection, and
// microsecond-level set operations on large bitmaps.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"cbitmap"
)

func main() {
	// 1. Sparse discrete IDs + dense contiguous ranges.
	a := cbitmap.New()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 100_000; i++ {
		a.Add(rng.Uint32() % 50_000_000) // sparse
	}
	a.AddRange(60_000_000, 70_000_000) // dense range -> RLE containers

	b := cbitmap.New()
	for i := 0; i < 100_000; i++ {
		b.Add(rng.Uint32() % 50_000_000)
	}
	b.AddRange(65_000_000, 80_000_000)

	fmt.Printf("bitmap A: %+v\n", a.Stats())
	fmt.Printf("bitmap B: %+v\n", b.Stats())

	// 2. Timed set operations.
	start := time.Now()
	inter := a.And(b)
	fmt.Printf("AND: %d values in %v\n", inter.Cardinality(), time.Since(start))

	start = time.Now()
	union := a.Or(b)
	fmt.Printf("OR : %d values in %v\n", union.Cardinality(), time.Since(start))

	start = time.Now()
	diff := a.Xor(b)
	fmt.Printf("XOR: %d values in %v\n", diff.Cardinality(), time.Since(start))

	// 3. Large-scale simulation: 1024 fully dense 64K blocks per bitmap
	//    (~67M values each); intersection/union complete in microseconds.
	x := cbitmap.New()
	y := cbitmap.New()
	for hi := uint32(0); hi < 1024; hi++ {
		x.AddRange(hi<<16, hi<<16|0xffff)
		y.AddRange(hi<<16, hi<<16|0xffff)
	}
	fmt.Printf("\nlarge-scale: %d values per bitmap\n", x.Cardinality())
	start = time.Now()
	n := x.And(y).Cardinality()
	fmt.Printf("AND: %d values in %v\n", n, time.Since(start))
	start = time.Now()
	n = x.Or(y).Cardinality()
	fmt.Printf("OR : %d values in %v\n", n, time.Since(start))
}

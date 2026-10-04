// Command demo exercises the compressed bitmap: sparse/dense loads,
// automatic container selection, batch set operations, and concurrent
// conflict arbitration.
package main

import (
	"fmt"
	"sync"
	"time"

	"bitmap"
)

func main() {
	// Sparse discrete ids + dense consecutive ranges.
	a := bitmap.New()
	a.Add(7)
	a.Add(42)
	a.AddRange(1_000_000, 2_000_000) // dense range -> RLE container

	b := bitmap.New()
	b.Add(42)
	b.AddRange(1_500_000, 3_000_000)

	fmt.Printf("a: %d ids\n", a.Cardinality())
	fmt.Printf("b: %d ids\n", b.Cardinality())
	fmt.Printf("AND: %d\n", bitmap.And(a, b).Cardinality())
	fmt.Printf("OR:  %d\n", bitmap.Or(a, b).Cardinality())
	fmt.Printf("XOR: %d\n", bitmap.Xor(a, b).Cardinality())

	arrays, bitmaps, runs := a.Stats()
	fmt.Printf("a containers: arrays=%d bitmaps=%d runs=%d\n", arrays, bitmaps, runs)

	// Multi-goroutine concurrent load through the conflict arbiter.
	shared := bitmap.New()
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100000; i++ {
				shared.Add(uint32(w*100000 + i))
			}
		}(w)
	}
	wg.Wait()
	fmt.Printf("concurrent load: %d ids in %s\n", shared.Cardinality(), time.Since(start))

	// Microsecond-scale batch op on compressible, large-space bitmaps.
	x := bitmap.New()
	y := bitmap.New()
	x.AddRange(0, 1_000_000_000)
	y.AddRange(500_000_000, 1_500_000_000)
	start = time.Now()
	res := bitmap.And(x, y)
	fmt.Printf("AND over 10^9-id dense bitmaps: %d ids in %s\n", res.Cardinality(), time.Since(start))
}

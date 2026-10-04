// Command example demonstrates the compressed bitmap with fine-grained
// metrics: sparse IDs, dense ranges, set operations, and JSON stats export.
package main

import (
	"fmt"
	"os"
	"time"

	"cbitmap"
)

func main() {
	m := cbitmap.NewMetrics()
	a := cbitmap.New(cbitmap.WithMetrics(m))
	b := cbitmap.New(cbitmap.WithMetrics(m))

	// Sparse discrete IDs.
	for _, id := range []uint32{7, 42, 1000, 65541, 1 << 20, 1<<31 - 1} {
		a.Add(id)
	}
	// Dense continuous ranges.
	must(a.AddRange(1<<16, 1<<16+100000)) // 100k consecutive IDs
	must(b.AddRange(1<<16+50000, 1<<16+150000))
	for _, id := range []uint32{42, 1 << 20, 1<<31 - 1} {
		b.Add(id)
	}

	fmt.Println("containers of a:", a.ContainerKinds())
	fmt.Println("containers of b:", b.ContainerKinds())
	fmt.Println("cardinality a:", a.Cardinality(), " b:", b.Cardinality())

	start := time.Now()
	inter := a.And(b)
	fmt.Printf("AND: %d values in %v\n", inter.Cardinality(), time.Since(start))

	start = time.Now()
	union := a.Or(b)
	fmt.Printf("OR:  %d values in %v\n", union.Cardinality(), time.Since(start))

	start = time.Now()
	diff := a.Xor(b)
	fmt.Printf("XOR: %d values in %v\n", diff.Cardinality(), time.Since(start))

	// Batch operations.
	must(a.And(b).AddRange(0, 0)) // no-op, keeps example simple
	batch := a.OrMany(b, inter, diff)
	fmt.Println("OR-many cardinality:", batch.Cardinality())

	// Large-scale simulation: 4 shards x 2.5e9 values = 1e10 (百亿) values.
	const shards = 4
	const perShard = uint64(2500000000)
	t0 := time.Now()
	var total uint64
	for s := 0; s < shards; s++ {
		x, y := cbitmap.New(), cbitmap.New()
		must(x.AddRange(0, perShard))
		must(y.AddRange(perShard/2, perShard/2+perShard))
		total += x.And(y).Cardinality()
	}
	fmt.Printf("simulated 1e10 values: intersected %d values in %v (%.0f us/op)\n",
		total, time.Since(t0), float64(time.Since(t0).Nanoseconds())/1e3/shards)

	// Lookups feed the hit-rate metric.
	for _, id := range []uint32{42, 43, 1 << 20, 123456789} {
		fmt.Printf("a.Contains(%d) = %v\n", id, a.Contains(id))
	}

	// An invalid range records an error code.
	if err := a.AddRange(100, 5); err != nil {
		fmt.Println("recorded error:", err)
	}

	fmt.Println("--- metrics snapshot (JSON) ---")
	if err := m.ExportJSON(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

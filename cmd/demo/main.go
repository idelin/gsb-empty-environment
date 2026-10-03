// Command demo is an end-to-end showcase of the compressed bitmap and the
// deterministic simulation / bug-reproduction framework.
package main

import (
	"fmt"
	"time"

	"bitmapsim/roaring"
	"bitmapsim/sim"
)

func printStats(name string, bm *roaring.Bitmap) {
	s := bm.Stats()
	fmt.Printf("  %-22s cardinality=%-12d containers=%-6d array=%d bitmap=%d run=%d bytes=%d\n",
		name, s.Cardinality, s.Containers, s.ArrayContainers, s.BitmapContainers, s.RunContainers, s.SizeInBytes)
}

func main() {
	fmt.Println("== 1. Automatic container selection ==")
	rng := sim.NewRNG(2024)
	sparse := roaring.New()
	for i := 0; i < 2000; i++ {
		sparse.Add(uint32(rng.Next() % (1 << 22)))
	}
	dense := roaring.New()
	dense.AddRange(1_000_000, 3_000_000)
	mixed := roaring.New()
	for i := 0; i < 30_000; i++ {
		mixed.Add(uint32(rng.Next() % (1 << 16)))
	}
	printStats("sparse ids", sparse)
	printStats("dense range", dense)
	printStats("dense random", mixed)

	fmt.Println("== 2. Set ops on billion-scale key space (RLE containers) ==")
	a := roaring.New()
	a.AddRange(0, 3_000_000_000)
	b := roaring.New()
	b.AddRange(1_000_000_000, 4_294_967_296)
	printStats("A: [0, 3e9)", a)
	printStats("B: [1e9, 2^32)", b)
	for _, op := range []struct {
		name string
		fn   func() *roaring.Bitmap
	}{
		{"AND", func() *roaring.Bitmap { return a.And(b) }},
		{"OR", func() *roaring.Bitmap { return a.Or(b) }},
		{"XOR", func() *roaring.Bitmap { return a.Xor(b) }},
	} {
		start := time.Now()
		res := op.fn()
		elapsed := time.Since(start)
		fmt.Printf("  A %s B: cardinality=%-13d took=%v\n", op.name, res.Cardinality(), elapsed)
	}

	fmt.Println("== 3. Deterministic simulation, 1,000,000 steps ==")
	cfg := sim.Config{Seed: 42, Steps: 1_000_000, NumBitmaps: 4, KeySpace: 1 << 16}
	start := time.Now()
	stats, fail := sim.New(cfg).Run()
	if fail != nil {
		fmt.Println("  unexpected failure:", fail)
		return
	}
	fmt.Printf("  events=%d adds=%d removes=%d queries=%d checkpoints=%d\n",
		stats.Events, stats.Adds, stats.Removes, stats.Queries, stats.Checkpoints)
	fmt.Printf("  hash=%x duration=%v\n", stats.Hash, time.Since(start))
	stats2, _ := sim.Reproduce(cfg)
	fmt.Printf("  reproduce: hash match=%v\n", stats.Hash == stats2.Hash)

	fmt.Println("== 4. Fault injection and exact reproduction ==")
	bad := sim.Config{Seed: 1234, Steps: 50_000, NumBitmaps: 4, KeySpace: 1 << 16, InjectFault: true, FaultStep: 25_000}
	_, fail1 := sim.New(bad).Run()
	fmt.Println("  first run:  ", fail1)
	_, fail2 := sim.Reproduce(bad)
	fmt.Println("  reproduced: ", fail2)
	fmt.Printf("  identical failure: %v\n", *fail1 == *fail2)
}

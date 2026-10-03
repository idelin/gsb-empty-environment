// Command demo exercises the compressed bitmap: container auto-selection,
// fast set operations, incremental snapshots and checkpoint archiving.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"bitmapckpt"
)

func main() {
	// 1. Insert sparse discrete IDs and dense contiguous ranges.
	bmA := bitmapckpt.New()
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 100000; i++ {
		bmA.Add(uint64(r.Intn(1 << 40))) // sparse
	}
	bmA.AddRange(5<<20, 5<<20+1<<18) // dense range: 262144 contiguous IDs
	bmA.AddRange(9<<20, 9<<20+80000)

	bmB := bitmapckpt.New()
	for i := 0; i < 80000; i++ {
		bmB.Add(uint64(r.Intn(1 << 40)))
	}
	bmB.AddRange(5<<20+100000, 5<<20+1<<18+50000)

	arrays, bitmaps, runs := bmA.ContainerStats()
	fmt.Printf("bitmap A: %d values, containers: array=%d bitmap=%d run=%d\n",
		bmA.Cardinality(), arrays, bitmaps, runs)
	arrays, bitmaps, runs = bmB.ContainerStats()
	fmt.Printf("bitmap B: %d values, containers: array=%d bitmap=%d run=%d\n",
		bmB.Cardinality(), arrays, bitmaps, runs)

	// 2. Boolean set operations.
	start := time.Now()
	inter := bmA.And(bmB)
	fmt.Printf("A AND B = %d values in %s\n", inter.Cardinality(), time.Since(start))
	start = time.Now()
	union := bmA.Or(bmB)
	fmt.Printf("A OR  B = %d values in %s\n", union.Cardinality(), time.Since(start))
	start = time.Now()
	diff := bmA.Xor(bmB)
	fmt.Printf("A XOR B = %d values in %s\n", diff.Cardinality(), time.Since(start))

	// 3. Simulate a ~10^10-value bitmap via RLE containers and intersect it
	//    with a sparse bitmap.
	huge := bitmapckpt.New()
	var n uint64 = 10000000000
	for k := uint64(0); k < n/65536; k++ {
		huge.AddRange(k<<16, (k+1)<<16)
	}
	fmt.Printf("huge bitmap: %d values in %d run containers\n",
		huge.Cardinality(), huge.NumContainers())
	sparse := bitmapckpt.New()
	for i := 0; i < 10000; i++ {
		sparse.Add(uint64(r.Intn(1 << 40)))
	}
	start = time.Now()
	hit := huge.And(sparse)
	fmt.Printf("huge(10^10) AND sparse(10^4) = %d values in %s\n",
		hit.Cardinality(), time.Since(start))

	// 4. Incremental snapshots + checkpoint archive.
	path := filepath.Join(os.TempDir(), "bitmapckpt-demo-archive.bin")
	defer os.Remove(path)
	arc, err := bitmapckpt.OpenArchive(path)
	if err != nil {
		panic(err)
	}
	if err := arc.AppendCheckpoint(bmA.Checkpoint()); err != nil {
		panic(err)
	}
	fmt.Println("checkpoint archived")

	for round := 0; round < 3; round++ {
		for i := 0; i < 5000; i++ {
			bmA.Add(uint64(r.Intn(1 << 40)))
		}
		start = time.Now()
		snap := bmA.Snapshot()
		fmt.Printf("round %d: incremental snapshot (%d dirty containers) in %s\n",
			round, len(snap.Cs), time.Since(start))
		if err := arc.AppendSnapshot(snap); err != nil {
			panic(err)
		}
	}
	if err := arc.Close(); err != nil {
		panic(err)
	}

	restored, err := bitmapckpt.RestoreFromArchive(path)
	if err != nil {
		panic(err)
	}
	fmt.Printf("restored from archive: %d values, equals source: %v\n",
		restored.Cardinality(), restored.Equals(bmA))
}

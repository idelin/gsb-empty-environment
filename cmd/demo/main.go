// Command demo exercises the roaring bitmap: sparse/dense ingestion,
// automatic container selection, boolean set algebra, versioned
// serialization, zero-copy restore and memory-mapped loading.
package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"roaring"
)

func main() {
	// --- 1. Ingest sparse ids and dense ranges ---------------------------
	a := roaring.New()
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 200000; i++ {
		a.Add(r.Uint32()) // sparse, scattered ids
	}
	a.AddRange(1000000000, 1000000000+5000000) // dense contiguous range
	a.AddRange(3000000000, 3000000000+2000000)

	b := roaring.New()
	for i := 0; i < 150000; i++ {
		b.Add(r.Uint32())
	}
	b.AddRange(1000000000+2500000, 1000000000+7500000)

	fmt.Println("== container selection ==")
	sa, sb := a.Stats(), b.Stats()
	fmt.Printf("A: %d values, %d containers (array=%d bitmap=%d run=%d), %d bytes serialized\n",
		sa.Cardinality, sa.Containers, sa.ArrayContainers, sa.BitmapContainers, sa.RunContainers, sa.SerializedBytes)
	fmt.Printf("B: %d values, %d containers (array=%d bitmap=%d run=%d), %d bytes serialized\n",
		sb.Cardinality, sb.Containers, sb.ArrayContainers, sb.BitmapContainers, sb.RunContainers, sb.SerializedBytes)

	// --- 2. Boolean set algebra ------------------------------------------
	fmt.Println("== set algebra ==")
	timeOp("A AND B", func() uint64 { return a.And(b).Cardinality() })
	timeOp("A OR  B", func() uint64 { return a.Or(b).Cardinality() })
	timeOp("A XOR B", func() uint64 { return a.Xor(b).Cardinality() })

	// --- 3. Persist, restore zero-copy, memory-map -----------------------
	fmt.Println("== persistence ==")
	buf := a.Serialize()
	start := time.Now()
	restored, err := roaring.Deserialize(buf)
	must(err)
	fmt.Printf("zero-copy deserialize of %d bytes: %v, equal=%v\n", len(buf), time.Since(start), restored.Equals(a))

	path := filepath.Join(os.TempDir(), "roaring-demo.bin")
	must(ioutil.WriteFile(path, buf, 0644))
	defer os.Remove(path)
	mapped, err := roaring.OpenMapped(path)
	must(err)
	fmt.Printf("mmap-opened %s: %d values, equal=%v\n", path, mapped.Cardinality(), mapped.Equals(a))
	mapped.Close()

	// --- 4. Large-scale simulation: full 32-bit key space ----------------
	// Two bitmaps each holding all 2^32 values, run-encoded (tiny memory),
	// intersected: ~8.6e9 logical bit positions per operation.
	fmt.Println("== large-scale simulation ==")
	x, y := roaring.New(), roaring.New()
	x.AddRange(0, 1<<32)
	y.AddRange(1<<31, 1<<32) // upper half twice -> still all values in [2^31, 2^32)
	y.AddRange(0, 1<<31)
	start = time.Now()
	inter := x.And(y)
	fmt.Printf("AND of two full-key-space bitmaps (2 x 2^32 values): %v -> %d values\n",
		time.Since(start), inter.Cardinality())
}

func timeOp(name string, fn func() uint64) {
	start := time.Now()
	n := fn()
	fmt.Printf("%-8s -> %12d values in %v\n", name, n, time.Since(start))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

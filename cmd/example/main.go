// Command example demonstrates the cbitmap package: building bitmaps with
// sparse and dense IDs, automatic container selection, set algebra,
// compact serialization, and zero-copy (mmap) deserialization.
package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"cbitmap"
)

func main() {
	// 1. Sparse discrete IDs.
	sparse := cbitmap.New()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000000; i++ {
		sparse.Add(uint32(rng.Intn(1 << 28)))
	}
	printStats("sparse (1e6 random IDs in [0, 2^28))", sparse)

	// 2. Dense contiguous ranges.
	dense := cbitmap.New()
	dense.AddRange(1<<20, 3<<20)   // 2M contiguous IDs
	dense.AddRange(5<<20, 5<<20+7) // tiny range
	dense.AddRange(8<<20, 12<<20)  // 4M contiguous IDs
	printStats("dense (contiguous ranges)", dense)

	// 3. Set algebra with timing.
	timeOp("AND", func() *cbitmap.Bitmap { return cbitmap.And(sparse, dense) })
	timeOp("OR ", func() *cbitmap.Bitmap { return cbitmap.Or(sparse, dense) })
	timeOp("XOR", func() *cbitmap.Bitmap { return cbitmap.Xor(sparse, dense) })

	// 4. Compact serialization.
	buf := sparse.MarshalBinary()
	fmt.Printf("serialized sparse bitmap: %d bytes (%.2f bits/value)\n",
		len(buf), 8*float64(len(buf))/float64(sparse.Cardinality()))

	// 5. Persist to file and restore zero-copy via mmap.
	dir, err := ioutil.TempDir("", "cbitmap-example")
	must(err)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "sparse.bin")
	must(ioutil.WriteFile(path, buf, 0644))

	start := time.Now()
	mapped, err := cbitmap.MmapFile(path)
	must(err)
	defer cbitmap.Munmap(mapped)
	frozen, err := cbitmap.UnmarshalZeroCopy(mapped)
	must(err)
	fmt.Printf("mmap + zero-copy restore: %v (frozen=%v)\n", time.Since(start), frozen.Frozen())

	// 6. Verify restored bitmap and run ops directly on mapped memory.
	if frozen.Cardinality() != sparse.Cardinality() {
		panic("cardinality mismatch after zero-copy restore")
	}
	start = time.Now()
	inter := cbitmap.And(frozen, dense)
	fmt.Printf("AND(mmap-restored, dense) = %d values in %v\n",
		inter.Cardinality(), time.Since(start))

	// 7. Frozen bitmaps use copy-on-write: the mapped file is never mutated.
	frozen.Add(1 << 30)
	fmt.Printf("after Add on frozen bitmap: frozen=%v card=%d (file untouched)\n",
		frozen.Frozen(), frozen.Cardinality())
}

func printStats(label string, bm *cbitmap.Bitmap) {
	s := bm.Stats()
	fmt.Printf("%s\n  values=%d containers=%d (array=%d bitmap=%d run=%d) payload=%d bytes\n",
		label, s.Cardinality, s.Containers,
		s.ArrayContainers, s.BitmapContainers, s.RunContainers, s.PayloadBytes)
}

func timeOp(label string, fn func() *cbitmap.Bitmap) {
	start := time.Now()
	out := fn()
	fmt.Printf("%s -> %d values in %v\n", label, out.Cardinality(), time.Since(start))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

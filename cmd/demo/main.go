// Command demo exercises the crset package: adaptive per-bucket encodings,
// cross-representation set operations, conversion hysteresis, and exact byte
// accounting.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"crset"
)

func printStats(name string, s *crset.Set) {
	st := s.Stats()
	naive := 4 * st.Elements
	fmt.Printf("%-24s %9d elems %5d buckets  %10d B  (naive []uint32: %10d B, %.1fx)\n",
		name, st.Elements, st.Buckets, st.TotalBytes, naive, float64(naive)/float64(st.TotalBytes))
	fmt.Printf("%-24s sparse %4d bkts/%10d B | bitmap %4d bkts/%10d B | run %4d bkts/%10d B | conversions %d\n",
		"", st.Sparse.Buckets, st.Sparse.Bytes, st.Bitmap.Buckets, st.Bitmap.Bytes,
		st.Run.Buckets, st.Run.Bytes, st.Conversions)
}

func main() {
	fmt.Println("== 1. Each high-16-bit bucket picks its cheapest encoding ==")
	start := time.Now()
	ranges := crset.New()
	for r := uint32(0); r < 200; r++ {
		for v := r * 5000; v < r*5000+1000; v++ {
			ranges.Add(v)
		}
	}
	dense := crset.New()
	for v := uint32(0); v < 4<<20; v += 2 { // all evens: runs too costly for RLE
		dense.Add(v)
	}
	rng := rand.New(rand.NewSource(1))
	sparse := crset.New()
	for i := 0; i < 5000; i++ {
		sparse.Add(uint32(rng.Intn(100000000)))
	}
	fmt.Printf("(built in %s)\n", time.Since(start).Round(time.Millisecond))
	printStats("ranges (200x1000 contig)", ranges)
	printStats("dense (evens in [0,4M))", dense)
	printStats("sparse (5k random in 1e8)", sparse)

	fmt.Println("\n== 2. Set operations across mixed encodings (verified vs map) ==")
	ref := map[uint32]bool{}
	for _, v := range dense.Elements() {
		ref[v] = true
	}
	start = time.Now()
	inter := ranges.Intersect(dense)
	union := ranges.Union(dense)
	diff := ranges.Difference(dense)
	elapsed := time.Since(start)
	wantInter := 0
	for _, v := range ranges.Elements() {
		if ref[v] {
			wantInter++
		}
	}
	ok := inter.Cardinality() == wantInter &&
		union.Cardinality() == ranges.Cardinality()+dense.Cardinality()-wantInter &&
		diff.Cardinality() == ranges.Cardinality()-wantInter
	fmt.Printf("intersect=%d union=%d difference=%d in %s, map-verified: %v\n",
		inter.Cardinality(), union.Cardinality(), diff.Cardinality(), elapsed.Round(time.Millisecond), ok)
	printStats("intersection result", inter)
	printStats("union result", union)

	fmt.Println("\n== 3. Hysteresis: hovering at the bitmap threshold does not flip-flop ==")
	hover, _ := crset.NewSet(crset.Config{SparseToBitmapCard: 4096, BitmapToSparseCard: 3584, RunSavingsFactor: 0})
	for i := 0; i <= 4096; i++ {
		hover.Add(uint32(3 * i)) // 4097 elements -> bitmap
	}
	convBefore := hover.Conversions()
	edge := uint32(3 * 4096)
	for k := 0; k < 20000; k++ {
		hover.Remove(edge) // 4096: inside hysteresis band
		hover.Add(edge)    // 4097: back above threshold
	}
	fmt.Printf("20000 remove/add pairs at card 4096/4097: conversions %d -> %d (0 extra), rep stays bitmap: %v\n",
		convBefore, hover.Conversions(), hover.Stats().Bitmap.Buckets == 1)

	fmt.Println("\n== 4. Thresholds are configurable ==")
	build := func(cfg crset.Config, contiguous bool) *crset.Set {
		s, err := crset.NewSet(cfg)
		if err != nil {
			panic(err)
		}
		for i := 0; i < 2000; i++ {
			if contiguous {
				s.Add(uint32(i))
			} else {
				s.Add(uint32(3 * i))
			}
		}
		return s
	}
	def := crset.DefaultConfig()
	noRun := crset.Config{SparseToBitmapCard: 4096, BitmapToSparseCard: 3584, RunSavingsFactor: 0}
	lowBitmap := crset.Config{SparseToBitmapCard: 1024, BitmapToSparseCard: 768, RunSavingsFactor: 0}
	printStats("stride-3, default cfg", build(def, false))
	printStats("stride-3, bitmap@1024", build(lowBitmap, false))
	printStats("contiguous, default", build(def, true))
	printStats("contiguous, RLE off", build(noRun, true))
}

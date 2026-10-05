// Command demo exercises the sparse2d index: it inserts points with
// duplicate coordinates, negative weights and huge sparse coordinates,
// performs atomic replaces, runs half-open rectangle queries and prints
// the index access statistics.
package main

import (
	"fmt"
	"math"
	"math/rand/v2"

	"sparse2d"
)

func main() {
	ix := sparse2d.New()
	rng := rand.New(rand.NewPCG(1, 2))

	// 100k random points over a huge sparse coordinate space, with
	// negative weights included.
	for i := 0; i < 100_000; i++ {
		id := fmt.Sprintf("p%d", i)
		x := int64(rng.Uint64() % (1 << 61))
		if i%2 == 0 {
			x = -x
		}
		y := int64(rng.Uint64() % (1 << 60))
		if i%3 == 0 {
			y = -y
		}
		w := int64(rng.IntN(2001)) - 1000 // weights in [-1000, 1000]
		ix.Add(id, x, y, w)
	}
	// duplicate coordinates under different IDs are charged separately
	ix.Add("dup-a", 7, 7, 5)
	ix.Add("dup-b", 7, 7, -3)
	// extreme coordinates
	ix.Add("edge-lo", math.MinInt64, math.MinInt64, 1)
	ix.Add("edge-hi", math.MaxInt64-1, math.MaxInt64-1, -1)
	// atomic replace: move dup-a far away with a new weight
	ix.Replace("dup-a", 1<<60, -(1 << 60), 42)
	ix.Remove("p0")

	fmt.Printf("points: %d, distinct x: %d\n", ix.Len(), ix.DistinctX())
	fmt.Printf("build stats: %+v\n", ix.Stats())

	queries := []struct {
		name           string
		x0, y0, x1, y1 int64
	}{
		{"whole space", math.MinInt64, math.MinInt64, math.MaxInt64, math.MaxInt64},
		{"first quadrant", 0, 0, math.MaxInt64, math.MaxInt64},
		{"origin box", -100, -100, 100, 100},
		{"dup column", 7, 7, 8, 8},
		{"thin strip", -(1 << 40), math.MinInt64, -(1<<40 - 1), math.MaxInt64},
		{"empty (x1<=x0)", 5, 5, 5, 9},
	}
	for _, q := range queries {
		ix.ResetStats()
		r := ix.Query(q.x0, q.y0, q.x1, q.y1)
		st := ix.Stats()
		fmt.Printf("%-18s count=%7d sum=%13d overflow=%-5v nodeVisits=%d\n",
			q.name, r.Count, r.Sum, r.Overflow, st.NodeVisits)
	}

	// overflow demonstration: two MaxInt64 weights
	ov := sparse2d.New()
	ov.Add("m1", 0, 0, math.MaxInt64)
	ov.Add("m2", 1, 1, math.MaxInt64)
	r := ov.Query(-1, -1, 2, 2)
	fmt.Printf("overflow demo        count=%7d sum=%13d overflow=%v (exact sum = 2^64-2)\n",
		r.Count, r.Sum, r.Overflow)

	fmt.Printf("cumulative query stats: %+v\n", ix.Stats())
}

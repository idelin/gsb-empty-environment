// Command demo exercises the sparse2d index with random online updates and
// prints per-query results together with index-access statistics.
package main

import (
	"fmt"
	"math"
	"math/rand"

	"sparse2d/sparse2d"
)

func main() {
	rng := rand.New(rand.NewSource(7))
	ix := sparse2d.New()

	// Online updates: coordinates appear in any order, including huge
	// sparse values near the int64 limits; weights may be negative.
	special := []sparse2d.Point{
		{ID: "far-low", X: math.MinInt64, Y: math.MinInt64, W: -50},
		{ID: "far-high", X: math.MaxInt64 - 1, Y: math.MaxInt64 - 1, W: 70},
		{ID: "dup-1", X: 5, Y: 5, W: 10},
		{ID: "dup-2", X: 5, Y: 5, W: -3}, // same cell, separate billing
	}
	for _, p := range special {
		must(ix.Add(p))
	}
	for i := 0; i < 2000; i++ {
		p := sparse2d.Point{
			ID: fmt.Sprintf("p%04d", i),
			X:  rng.Int63n(1_000_000) - 500_000,
			Y:  rng.Int63n(1_000_000) - 500_000,
			W:  rng.Int63n(2001) - 1000,
		}
		must(ix.Add(p))
	}
	// Atomic replace: move one point and change its weight in one step.
	must(ix.Replace(sparse2d.Point{ID: "p0000", X: 0, Y: 0, W: math.MaxInt64}))
	must(ix.Remove("p1999"))

	queries := []sparse2d.Rect{
		{MinX: -100, MinY: -100, MaxX: 100, MaxY: 100},
		{MinX: math.MinInt64, MinY: math.MinInt64, MaxX: math.MaxInt64, MaxY: math.MaxInt64},
		{MinX: 5, MinY: 5, MaxX: 6, MaxY: 6},
		{MinX: -1, MinY: -1, MaxX: 1, MaxY: 1},
	}
	for i, r := range queries {
		res, err := ix.Query(r)
		if err != nil {
			panic(err)
		}
		fmt.Printf("query %d %+v\n", i, r)
		fmt.Printf("  count=%d sum=%d overflow=%v\n", res.Count, res.Sum, res.Overflow)
		fmt.Printf("  stats: x-nodes=%d fenwick-reads=%d\n",
			res.Stats.XNodesVisited, res.Stats.FenwickReads)
		// Cross-check against the full scan.
		ref, _ := ix.Scan(r)
		if ref.Count != res.Count || ref.Sum != res.Sum || ref.Overflow != res.Overflow {
			panic(fmt.Sprintf("MISMATCH vs full scan: query=%+v scan=%+v", res, ref))
		}
		fmt.Println("  full-scan check: OK")
	}

	t := ix.Stats()
	fmt.Printf("\ncumulative: queries=%d mutations=%d x-nodes=%d fenwick-reads=%d "+
		"fenwick-writes=%d slot-rebuilds=%d\n",
		t.Queries, t.Mutations, t.XNodesVisited, t.FenwickReads,
		t.FenwickWrites, t.SlotRebuilds)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

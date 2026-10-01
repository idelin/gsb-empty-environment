// Demo of the cwi compressed, versioned weight index.
package main

import (
	"fmt"
	"math/rand"

	"cwi"
)

func main() {
	rng := rand.New(rand.NewSource(7))
	const n = 1000
	ws := make([]uint64, n)
	for i := range ws {
		switch rng.Intn(5) {
		case 0:
			ws[i] = 0 // zero plateaus
		default:
			ws[i] = uint64(rng.Intn(500))
		}
	}

	ix, err := cwi.New(ws)
	if err != nil {
		panic(err)
	}
	fmt.Printf("encoding layout : %s\n", cwi.Layout())
	fmt.Printf("n=%d weights, block size=%d\n", ix.Len(), cwi.BlockSize)
	b0, _ := ix.StoredBytes(0)
	fmt.Printf("stored bytes v0 : %d (naive []uint64: %d)\n", b0, 8*n)
	tot, _ := ix.Total(0)
	fmt.Printf("total weight v0 : %d\n\n", tot)

	// Batch range increments; each commits a new version atomically.
	updates := []struct {
		lo, hi int
		delta  int64
	}{
		{100, 200, 50},
		{150, 900, 30},
		{0, 1000, 7},
	}
	for _, u := range updates {
		rep, err := ix.RangeAdd(u.lo, u.hi, u.delta)
		if err != nil {
			fmt.Printf("RangeAdd(%d,%d,%+d) rejected: %v\n", u.lo, u.hi, u.delta, err)
			continue
		}
		fmt.Printf("RangeAdd(%4d,%4d,%+3d) -> v%d, re-encoded blocks [%d..%d] (%d blocks, %d bytes)\n",
			u.lo, u.hi, u.delta, rep.Version, rep.FirstBlock, rep.LastBlock, rep.BlocksReencoded, rep.BytesWritten)
	}

	// A negative update that would drive a weight below zero is rejected
	// atomically: no new version, no partial change.
	if _, err := ix.RangeAdd(0, n, -1000); err != nil {
		fmt.Printf("RangeAdd(0,%d,-1000) rejected as expected: %v (versions still %d)\n", n, err, ix.Versions())
	}
	fmt.Println()

	// Queries on the latest version, with block-access accounting. The last
	// two targets hit the "target >= total -> past-the-end" convention.
	latest, _ := ix.Total(ix.Versions() - 1)
	for _, target := range []uint64{0, latest / 4, latest / 2, latest - 1, latest, latest + 1} {
		sel, st, _ := ix.Select(ix.Versions()-1, target)
		fmt.Printf("Select(latest, %8d) -> index %4d (blocks decoded: %d)\n", target, sel, st.BlocksDecoded)
	}
	for _, i := range []int{0, 137, 512, n} {
		ps, st, _ := ix.PrefixSum(ix.Versions()-1, i)
		fmt.Printf("PrefixSum(latest, %4d) = %8d (blocks decoded: %d)\n", i, ps, st.BlocksDecoded)
	}
	fmt.Println()

	// Old versions remain fully queryable by cumulative weight.
	oldTot, _ := ix.Total(0)
	sel, st, _ := ix.Select(0, oldTot/2)
	fmt.Printf("Select(v0, %d) -> index %d (blocks decoded: %d)\n", oldTot/2, sel, st.BlocksDecoded)
	fmt.Printf("stored bytes all versions: %d (naive per-version []uint64: %d)\n",
		ix.TotalStoredBytes(), ix.NaiveBytes())
}

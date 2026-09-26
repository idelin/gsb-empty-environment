// Command wtdemo builds a wavelet tree over a sample array (negatives and
// duplicates included), prints the real physical layout and byte footprint,
// and runs example access / quantile / range-frequency queries.
package main

import (
	"fmt"

	wt "wavelettree"
)

func main() {
	vals := []int{-5, 3, -5, 7, 3, 3, -2, 7, 7, 7, 0, -5}
	fmt.Println("input:", vals)

	tr, err := wt.New(vals)
	if err != nil {
		panic(err)
	}

	fmt.Println("\n== physical layout ==")
	s := tr.Stats()
	fmt.Printf("elements n            : %d\n", tr.Len())
	fmt.Printf("tree levels (stored)  : %d\n", s.Levels)
	fmt.Printf("tree nodes            : %d\n", s.Nodes)
	fmt.Printf("bitvector payload     : %d bits in %d bytes (packed []uint64)\n",
		s.BitvectorBits, s.BitvectorBytes)
	fmt.Printf("rank index            : %d bytes ([]uint32, one entry per 64-bit word)\n",
		s.RankIndexBytes)
	fmt.Printf("value table (id->val) : %d bytes (sorted distinct originals)\n",
		s.ValueTableBytes)
	fmt.Printf("index total           : %d bytes\n", s.Total())
	fmt.Printf("raw []int64 baseline  : %d bytes (payload only, no queries possible)\n",
		8*len(vals))

	fmt.Println("\n== access (original order preserved) ==")
	for i := 0; i < tr.Len(); i++ {
		v, _ := tr.Access(i)
		fmt.Printf("Access(%2d) = %d\n", i, v)
	}

	fmt.Println("\n== quantile: k-th smallest in [l, r], duplicates counted ==")
	for _, q := range [][3]int{{0, 11, 1}, {0, 11, 3}, {0, 11, 6}, {3, 9, 4}, {0, 11, 12}} {
		v, err := tr.Quantile(q[0], q[1], q[2])
		fmt.Printf("Quantile(l=%2d, r=%2d, k=%2d) = %d  (err=%v)\n", q[0], q[1], q[2], v, err)
	}
	if _, err := tr.Quantile(5, 4, 1); err != nil {
		fmt.Printf("Quantile(l=5, r=4, k=1) rejected as expected: %v\n", err)
	}

	fmt.Println("\n== range frequency: count of values in [a, b] over positions [l, r] ==")
	for _, q := range [][4]int{{0, 11, -5, -5}, {0, 11, 3, 7}, {3, 9, 7, 7}, {0, 11, 1, 2}, {0, 11, -100, 100}} {
		c, err := tr.RangeFreq(q[0], q[1], q[2], q[3])
		fmt.Printf("RangeFreq(l=%2d, r=%2d, a=%4d, b=%4d) = %d  (err=%v)\n",
			q[0], q[1], q[2], q[3], c, err)
	}
}

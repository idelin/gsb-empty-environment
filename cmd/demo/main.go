// Demo of the compressed hierarchical partial-sum index.
package main

import (
	"fmt"
	"math/rand"

	"cidx"
)

func main() {
	const n = 1_000_000
	rng := rand.New(rand.NewSource(42))

	// Build: random weights via batch range increments.
	v0 := cidx.New(n)
	var ops []cidx.RangeOp
	for i := 0; i < n; {
		span := 1 + rng.Intn(37)
		if i+span > n {
			span = n - i
		}
		ops = append(ops, cidx.RangeOp{Lo: i, Hi: i + span, Delta: int64(rng.Intn(100))})
		i += span
	}
	v1, rep, err := v0.BatchAdd(ops)
	must(err)
	fmt.Printf("built n=%d, total=%d\n", n, v1.Total())
	fmt.Printf("build report: blocks re-encoded=%d range=[%d,%d) element-updates=%d\n",
		rep.BlocksTouched, rep.ReencodeLo, rep.ReencodeHi, rep.ElementUpdates)

	// Encoding layout and footprint.
	fmt.Printf("\nencoding layout per block: count u16 | width u8 | base u64 | total u64 | bit-packed deltas\n")
	fmt.Printf("blocks=%d block-bytes[0..3]=%d %d %d %d\n",
		v1.NumBlocks(), v1.BlockBytes(0), v1.BlockBytes(1), v1.BlockBytes(2), v1.BlockBytes(3))
	fmt.Printf("compressed index: %d bytes  (naive []uint64: %d bytes, ratio %.2fx)\n",
		v1.Bytes(), n*8, float64(n*8)/float64(v1.Bytes()))

	// Prefix and select with access stats.
	ps, pst := v1.PrefixStats(n / 2)
	fmt.Printf("\nprefix(n/2)=%d  (blocks accessed=%d, fenwick nodes=%d)\n",
		ps, pst.BlocksAccessed, pst.FenwickNodes)
	for _, target := range []uint64{0, v1.Total() / 4, v1.Total() / 2, v1.Total() - 1, v1.Total()} {
		idx, st := v1.SelectStats(target)
		fmt.Printf("select(%d) -> %d  (blocks accessed=%d, fenwick nodes=%d)\n",
			target, idx, st.BlocksAccessed, st.FenwickNodes)
	}

	// Local update: repair only the covering block + its fenwick ancestors.
	v2, rep2, err := v1.BatchAdd([]cidx.RangeOp{{Lo: 123_456, Hi: 123_556, Delta: 7}})
	must(err)
	fmt.Printf("\nlocal update [123456,123556)+7: re-encoded blocks [%d,%d) (%d blocks)\n",
		rep2.ReencodeLo, rep2.ReencodeHi, rep2.BlocksTouched)

	// Persistence: old version still answers select-by-cumulative-weight.
	fmt.Printf("old v1 total=%d select(total-1)=%d ; new v2 total=%d select(total-1)=%d\n",
		v1.Total(), v1.Select(v1.Total()-1), v2.Total(), v2.Select(v2.Total()-1))

	// Atomic rejection: negative weight attempt, old version untouched.
	_, _, err = v2.BatchAdd([]cidx.RangeOp{{Lo: 0, Hi: n, Delta: -1 << 62}})
	fmt.Printf("\nnegative batch rejected: %v ; v2 total still %d\n", err, v2.Total())

	// Zero plateau: zero-weight elements are never selected.
	z := cidx.New(9)
	for i, w := range []uint64{0, 0, 0, 5, 0, 0, 3, 0, 0} {
		z, err = z.Set(i, w)
		must(err)
	}
	fmt.Printf("\nzero plateau [0,0,0,5,0,0,3,0,0]: select(0)=%d select(4)=%d select(5)=%d select(8)=%d (past-the-end)\n",
		z.Select(0), z.Select(4), z.Select(5), z.Select(8))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

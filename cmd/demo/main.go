// Demo: copy-on-write bitmap snapshots, lazy AND/OR, and byte accounting.
package main

import (
	"fmt"

	cow "cowbitmap"
)

func main() {
	const nbits = 1_000_000
	nblocks := (nbits + cow.BlockBits - 1) / cow.BlockBits
	fmt.Printf("bitmap: %d bits, %d blocks x %d bits (%d B/block)\n\n",
		nbits, nblocks, cow.BlockBits, cow.BlockBytes)

	// v1: a sparse bitmap.
	v1 := cow.New(nbits)
	for i := 0; i < nbits; i += 997 {
		v1.Set(i, true)
	}
	fmt.Printf("v1: popcount=%d  mem=%d B  encoded=%d B (naive bitmap would be %d B)\n",
		v1.Count(), v1.MemBytes(), v1.EncodedLen(), nbits/8)

	// Snapshot, then flip bits in v2 only.
	v2 := v1.Snapshot()
	fmt.Printf("\nafter Snapshot: v2 shares %d blocks with v1, v2 mem=%d B (no copy)\n",
		v2.SharedBlocks(), v2.MemBytes())
	for i := 0; i < nbits; i += 4999 {
		v2.Set(i, true)
	}
	v2.Set(0, false) // flip a bit that v1 has set
	fmt.Printf("after %d flips in v2: v2 shares %d blocks, v1 popcount=%d (unchanged)\n",
		nbits/4999+1, v2.SharedBlocks(), v1.Count())
	b0v1, _ := v1.Get(0)
	b0v2, _ := v2.Get(0)
	fmt.Printf("bit 0: v1=%v v2=%v  (old snapshot isolated from new flips)\n", b0v1, b0v2)

	// Lazy AND/OR: nothing is computed until bits are read.
	and, _ := cow.And(v1, v2)
	or, _ := cow.Or(v1, v2)
	fmt.Printf("\nlazy AND/OR created: materialized blocks = %d (nothing computed yet)\n",
		and.MaterializedBlocks())
	hit, _ := and.Get(997 * 3)
	fmt.Printf("and.Get(%d)=%v -> materialized blocks now %d (computed on demand)\n",
		997*3, hit, and.MaterializedBlocks())
	andFull, _ := and.Materialize()
	orFull, _ := or.Materialize()
	fmt.Printf("AND popcount=%d  OR popcount=%d  (v1=%d, v2=%d)\n",
		andFull.Count(), orFull.Count(), v1.Count(), v2.Count())

	// Length mismatch is rejected before any work.
	short := cow.New(1000)
	if _, err := cow.And(v1, short); err != nil {
		fmt.Printf("\nAnd(v1, len=1000) rejected upfront: %v\n", err)
	}

	// Reference reclamation: release everything, double release is an error.
	and.Release()
	or.Release()
	andFull.Release()
	orFull.Release()
	v1.Release()
	v2.Release()
	fmt.Printf("all released; second v2.Release() -> %v\n", v2.Release())

	// Encoding round-trip.
	w := cow.New(10000)
	w.Set(0, true)
	w.Set(511, true)
	w.Set(512, true)
	w.Set(9999, true)
	enc := w.Encode()
	back, _ := cow.Decode(enc)
	fmt.Printf("\nencoding: %d bits -> %d B (28 B header + 3 unique blocks x 64 B + 20 x 4 B table)\n",
		w.Len(), len(enc))
	fmt.Printf("round-trip: popcount=%d, bit9999=%v\n", back.Count(), mustGet(back, 9999))
}

func mustGet(m *cow.Bitmap, bit int) bool {
	v, err := m.Get(bit)
	if err != nil {
		panic(err)
	}
	return v
}

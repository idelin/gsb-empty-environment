// Command demo exercises the persistent bitmap: block-level copy-on-write,
// snapshots, lazy AND/OR, reference reclamation and the binary encoding.
package main

import (
	"bytes"
	"fmt"

	"bitmapdemo/bitmap"
)

func main() {
	const nbits = 1 << 20 // 1 Mi bits = 256 blocks of 4096 bits
	fmt.Printf("block geometry: %d bits/block, %d bytes/block, %d blocks total\n",
		bitmap.BlockBits, bitmap.BlockBytes, nbits/bitmap.BlockBits)

	a := bitmap.New(nbits)
	a.Set(7, true)
	a.Set(5000, true)
	a.Set(900_000, true)
	fmt.Printf("\na: %d bits set, %d non-empty blocks\n", a.Count(), a.MaterializedBlocks())

	// Snapshot shares every block; a flip copies exactly one 512-byte block.
	snap := a.Snapshot()
	a.Set(8, true)
	a.Set(7, false)
	fmt.Printf("snapshot after flips of a: count=%d (unchanged), a.count=%d\n",
		snap.Count(), a.Count())
	st := snap.MemStats()
	fmt.Printf("shared graph: %d headers + %d blocks + %d dir slots = %d bytes live\n",
		st.Bitmaps, st.Blocks, st.DirSlots, st.TotalByte)

	// Lazy AND: nothing is computed until a block is touched.
	b := bitmap.New(nbits)
	b.Set(8, true)
	b.Set(5000, true)
	b.Set(700_000, true)
	and, err := bitmap.And(a, b)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nlazy AND created: materialized blocks = %d (nothing computed yet)\n",
		and.MaterializedBlocks())
	fmt.Printf("AND.Get(8)=%v AND.Get(5000)=%v -> materialized blocks = %d\n",
		and.Get(8), and.Get(5000), and.MaterializedBlocks())
	fmt.Printf("AND popcount (forces full materialization) = %d, blocks = %d\n",
		and.Count(), and.MaterializedBlocks())

	// Length mismatch is rejected before any node or reference is created.
	short := bitmap.New(1024)
	if _, err := bitmap.And(a, short); err != nil {
		fmt.Printf("\nmismatched AND rejected upfront: %v\n", err)
	}
	short.Release()

	// Real encoding layout: header + directory + non-empty block payloads.
	var buf bytes.Buffer
	if err := a.Encode(&buf); err != nil {
		panic(err)
	}
	fmt.Printf("\nencoding of a: %d bytes = 20 header + 256*8 directory + %d*512 payloads\n",
		buf.Len(), a.MaterializedBlocks())
	fmt.Printf("EncodedSize() agrees: %d bytes (vs %d bytes as a raw bitset)\n",
		a.EncodedSize(), nbits/8)

	// Reference reclamation: everything returns to the baseline.
	fmt.Printf("\nlive blocks before release: %d\n", bitmap.LiveBlocks())
	and.Release()
	and.Release() // repeated release is a no-op, refs never go negative
	snap.Release()
	a.Release()
	b.Release()
	fmt.Printf("live blocks after release:  %d (double release kept refs >= 0)\n",
		bitmap.LiveBlocks())
}

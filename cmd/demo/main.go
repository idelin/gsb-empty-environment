// Command demo encodes a realistic int64 series with the bitpack
// archive format, prints the real encoded layout and byte costs,
// and demonstrates random access, range decode and corruption isolation.
package main

import (
	"fmt"
	"math"
	"math/rand"

	"bitpack"
)

func main() {
	rng := rand.New(rand.NewSource(2026))
	const n = 1000
	const blockSize = 128
	vals := make([]int64, n)
	for i := range vals {
		vals[i] = 1000 + int64(rng.Intn(41)-20) // sensor-like jitter around 1000
	}
	for i := 200; i < 260; i++ {
		vals[i] = 0 // a silent stretch: zero block
	}
	vals[511] = math.MaxInt64 // one huge outlier -> out-of-band exception
	vals[900] = -5000         // negative outlier

	data := bitpack.Encode(vals, blockSize)
	r, err := bitpack.Open(data)
	if err != nil {
		panic(err)
	}

	fmt.Printf("values: %d, block size: %d, blocks: %d\n", r.Len(), blockSize, r.NumBlocks())
	fmt.Printf("encoded size: %d bytes (raw int64: %d bytes, ratio %.2fx)\n",
		r.Size(), n*8, float64(n*8)/float64(r.Size()))
	fmt.Printf("header: %d bytes, index: %d bytes\n", bitpack.HeaderSize, r.NumBlocks()*12)

	layout, err := r.Layout()
	if err != nil {
		panic(err)
	}
	fmt.Println("\nblock layout (real on-disk bytes):")
	fmt.Println(" idx  off  count  width  base   exc  bitmap  payload  record")
	for _, bi := range layout {
		fmt.Printf(" %3d %5d  %4d  %4d  %6d  %3d  %5v  %6d  %6d\n",
			bi.Index, bi.Offset, bi.Count, bi.Width, bi.Base,
			bi.Exceptions, bi.Bitmap, bi.PayloadSize, bi.RecordSize)
	}

	// Random access: single value, only its block is decoded.
	v, err := r.At(511)
	fmt.Printf("\nrandom access At(511) = %d (err=%v)\n", v, err)

	// Range decode across a block boundary.
	rg, failed := r.DecodeRange(120, 140)
	ok := len(failed) == 0 && len(rg) == 20 && rg[0] == vals[120] && rg[19] == vals[139]
	fmt.Printf("range decode [120,140): %d values, matches original: %v\n", len(rg), ok)

	// Corruption isolation: flip one byte in block 3, others still decode.
	layout3 := layout[3]
	data[layout3.Offset+4+10] ^= 0xFF
	r2, err := bitpack.Open(data)
	if err != nil {
		panic(err)
	}
	dec, failed := r2.DecodeRange(0, n)
	fmt.Printf("after 1-byte tamper in block 3: failed blocks=%v, recovered %d/%d values\n",
		failed, len(dec), n)
}

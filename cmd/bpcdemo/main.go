// Command bpcdemo encodes a signed sensor-like signal with bitplane and
// prints honest size statistics (header included) plus a random-access
// range decode sample.
package main

import (
	"fmt"
	"math"
	"math/rand"

	"bitplane"
)

func main() {
	// 4096 samples of a slowly varying signed signal, tenths of a degree.
	rng := rand.New(rand.NewSource(1))
	const n = 4096
	vals := make([]int64, n)
	for i := range vals {
		v := 200 + 150*math.Sin(float64(i)/200) + rng.NormFloat64()*8
		vals[i] = int64(math.Round(v))
	}

	buf, stats, err := bitplane.Encode(vals, bitplane.Options{Width: 16, Signed: true})
	if err != nil {
		panic(err)
	}

	fmt.Println("=== bitplane demo: 4096 signed int64 samples, width=16, zigzag ===")
	fmt.Printf("values:            %d\n", stats.Values)
	fmt.Printf("block size:        %d values (%d blocks)\n", stats.BlockSize, stats.NumBlocks)
	fmt.Printf("header:            %d bytes\n", stats.HeaderBytes)
	fmt.Printf("index:             %d bytes\n", stats.IndexBytes)
	fmt.Printf("data:              %d bytes\n", stats.DataBytes)
	fmt.Printf("total:             %d bytes\n", stats.TotalBytes)
	fmt.Printf("raw int64:         %d bytes\n", stats.RawBytes)
	fmt.Printf("ratio vs int64:    %.3f\n", stats.Ratio())
	fmt.Printf("plane chunks:      literal=%d rle=%d delta=%d\n",
		stats.ModeCounts[0], stats.ModeCounts[1], stats.ModeCounts[2])

	// Full roundtrip check.
	back, err := bitplane.Decode(buf, n)
	if err != nil {
		panic(err)
	}
	for i := range vals {
		if back[i] != vals[i] {
			panic(fmt.Sprintf("roundtrip mismatch at %d", i))
		}
	}
	fmt.Println("roundtrip:         OK (all 4096 values identical)")

	// Random access: decode only [1000, 1010) without touching other blocks.
	const start, end = 1000, 1010
	slice, err := bitplane.DecodeRange(buf, start, end, end-start)
	if err != nil {
		panic(err)
	}
	fmt.Printf("range [%d,%d):     %v\n", start, end, slice)

	// Quota enforcement demo.
	if _, err := bitplane.Decode(buf, 10); err != nil {
		fmt.Printf("quota(10):         rejected as expected: %v\n", err)
	}
}

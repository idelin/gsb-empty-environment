// Command bpc demonstrates the bitplane package: it encodes a synthetic
// signed sensor-like series, reports compression overhead (headers and
// index included), and performs random range decoding.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"bitplane"
)

func main() {
	rng := rand.New(rand.NewSource(42))
	n := 200000
	vals := make([]int64, n)
	cur := int64(1000)
	for i := range vals {
		cur += int64(rng.Intn(21) - 10) // random walk, negative steps included
		if rng.Intn(2000) == 0 {
			cur += int64(rng.Intn(40001) - 20000) // rare spike
		}
		vals[i] = cur
	}

	start := time.Now()
	data, err := bitplane.EncodeBytes(vals, bitplane.Options{Bits: 32, Signed: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
	encDur := time.Since(start)

	raw := n * 4 // fixed-width 32-bit source
	fmt.Printf("values        : %d (int32, signed)\n", n)
	fmt.Printf("raw size      : %d bytes\n", raw)
	fmt.Printf("compressed    : %d bytes (header + index + blocks + trailer)\n", len(data))
	fmt.Printf("ratio         : %.2fx\n", float64(raw)/float64(len(data)))
	fmt.Printf("encode time   : %v\n", encDur)

	// Full decode with a bounded output quota.
	start = time.Now()
	all, hdr, err := bitplane.DecodeAll(data, n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decode:", err)
		os.Exit(1)
	}
	fmt.Printf("decode time   : %v\n", time.Since(start))
	fmt.Printf("header        : version=%d bits=%d signed=%v count=%d blockSize=%d blocks=%d\n",
		hdr.Version, hdr.Bits, hdr.Signed, hdr.Count, hdr.BlockSize, hdr.NumBlocks)
	ok := len(all) == n
	for i := range all {
		if all[i] != vals[i] {
			ok = false
			break
		}
	}
	fmt.Printf("round trip    : %v\n", ok)

	// Random range decode: only the overlapping blocks are touched.
	lo, hi := 150000, 150008
	part, _, err := bitplane.DecodeRange(data, lo, hi, 1024)
	if err != nil {
		fmt.Fprintln(os.Stderr, "range decode:", err)
		os.Exit(1)
	}
	match := true
	for i, v := range part {
		if v != vals[lo+i] {
			match = false
		}
	}
	fmt.Printf("range [%d,%d) : %v (match=%v)\n", lo, hi, part, match)
}

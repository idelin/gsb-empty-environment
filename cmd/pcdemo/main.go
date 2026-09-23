// Command pcdemo demonstrates building a length-limited prefix code,
// checking the Kraft inequality, reporting weighted code length cost,
// and round-tripping a message through the self-describing binary format.
package main

import (
	"flag"
	"fmt"
	"os"

	"prefixcodec/prefixcode"
)

func main() {
	maxLen := flag.Int("L", 5, "maximum code length")
	limit := flag.Int("limit", -1, "decode output bound in symbols (-1 = unbounded)")
	flag.Parse()

	// Symbol frequencies (symbols 0..7).
	freqs := []uint64{45, 16, 13, 12, 9, 5, 3, 2}
	fmt.Printf("symbols=%d frequencies=%v maxLen=%d\n\n", len(freqs), freqs, *maxLen)

	code, err := prefixcode.BuildCode(freqs, *maxLen)
	if err != nil {
		fmt.Println("REFUSED:", err)
		os.Exit(1)
	}

	fmt.Println("symbol  freq  len  codeword")
	for sym := range freqs {
		bits, length, _ := code.Bits(sym)
		fmt.Printf("%5d  %4d  %3d  %0*b\n", sym, freqs[sym], length, length, bits)
	}

	num, complete := code.Kraft()
	fmt.Printf("\nKraft sum = %d/2^%d = %.6f (<= 1, complete=%v)\n",
		num, *maxLen, float64(num)/float64(uint64(1)<<uint(*maxLen)), complete)
	total, avg := code.Cost(freqs)
	fmt.Printf("weighted cost = %d bits, average = %.4f bits/symbol\n", total, avg)

	// Round-trip a message through the binary format.
	message := []int{0, 0, 1, 2, 0, 3, 4, 0, 5, 6, 7, 0, 1, 0}
	blob, err := prefixcode.Encode(code, message)
	if err != nil {
		fmt.Println("encode failed:", err)
		os.Exit(1)
	}
	fmt.Printf("\nencoded %d symbols into %d bytes (format PCD1: version+lengths+payload+CRC32)\n", len(message), len(blob))

	decCode, decoded, err := prefixcode.Decode(blob, *limit)
	if err != nil {
		fmt.Println("decode failed:", err)
		os.Exit(1)
	}
	fmt.Printf("decoded %d symbols, tables equal=%v, round-trip ok=%v\n",
		len(decoded), equalLens(code.Lengths, decCode.Lengths), equalInts(message, decoded))

	// 1) Refusal when 2^L < symbol count.
	if _, err := prefixcode.BuildCode(freqs, 2); err != nil {
		fmt.Printf("\n[infeasible] L=2 with %d symbols -> %v\n", len(freqs), err)
	}

	// 2) Corruption detection via CRC32.
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-6] ^= 0xFF
	if _, _, err := prefixcode.Decode(tampered, -1); err != nil {
		fmt.Printf("[tamper] flipped payload byte -> %v\n", err)
	}

	// 3) Bounded decode output.
	if _, _, err := prefixcode.Decode(blob, len(message)-1); err != nil {
		fmt.Printf("[bounded] limit=%d symbols -> %v\n", len(message)-1, err)
	}
}

func equalLens(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

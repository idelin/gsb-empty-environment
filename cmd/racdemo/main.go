// Command racdemo demonstrates the rangecoder package: it compresses a
// sample text, prints the container header fields, and verifies the
// round trip under an explicit output budget.
package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"strings"

	"rangecoder"
)

func main() {
	sample := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog. ", 40))
	sample = append(sample, []byte("Pack my box with five dozen liquor jugs! 0123456789")...)

	enc := rangecoder.Encode(sample)

	fmt.Printf("format version : %d\n", enc[3])
	fmt.Printf("declared length: %d bytes\n", binary.BigEndian.Uint64(enc[4:12]))
	fmt.Printf("declared crc32 : %#08x\n", binary.BigEndian.Uint32(enc[12:16]))
	fmt.Printf("original size  : %d bytes\n", len(sample))
	fmt.Printf("encoded size   : %d bytes (%.1f%% of original)\n",
		len(enc), 100*float64(len(enc))/float64(len(sample)))

	const budget = 1 << 20 // hard cap on decoded output
	dec, err := rangecoder.Decode(enc, budget)
	if err != nil {
		fmt.Println("decode failed:", err)
		os.Exit(1)
	}
	if string(dec) != string(sample) {
		fmt.Println("round trip MISMATCH")
		os.Exit(1)
	}
	fmt.Printf("round trip     : OK, crc32 %#08x verified within budget %d\n",
		crc32.ChecksumIEEE(dec), budget)
}

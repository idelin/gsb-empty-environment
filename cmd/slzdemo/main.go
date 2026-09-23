// Command slzdemo demonstrates the slz sliding-window LZ codec:
// it compresses a sample text, verifies the round trip, compares the
// greedy and optimal parsers, and shows chunked (checkpointed) encoding.
package main

import (
	"fmt"
	"os"
	"strings"

	"slz"
)

func main() {
	sample := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 40)

	enc := slz.Compress([]byte(sample))
	dec, err := slz.Decompress(enc, 1<<20)
	if err != nil {
		fmt.Println("decode error:", err)
		os.Exit(1)
	}
	fmt.Printf("sample:      %d bytes\n", len(sample))
	fmt.Printf("compressed:  %d bytes (%.1f%%)\n", len(enc), 100*float64(len(enc))/float64(len(sample)))
	fmt.Printf("round trip:  %v\n", string(dec) == sample)

	// Greedy longest-match is not optimal: fixed counterexample.
	ce := "abcxbcdefgabcdefg"
	fmt.Printf("counterexample %q: greedy=%d bits, optimal=%d bits\n",
		ce, slz.GreedyBits([]byte(ce)), slz.OptimalBits([]byte(ce)))

	// Chunked encoding: each block boundary is a checkpoint; blocks share
	// the sliding window unless it is reset.
	sizes := []int{500, 500, 500, len(sample) - 1500}
	chunked := slz.CompressBlocks([]byte(sample), sizes, false)
	dec2, err := slz.Decompress(chunked, 1<<20)
	if err != nil {
		fmt.Println("chunked decode error:", err)
		os.Exit(1)
	}
	fmt.Printf("chunked (%d blocks, shared window): %d bytes, round trip: %v\n",
		len(sizes), len(chunked), string(dec2) == sample)

	// Distance-1 run: overlapping copy extends correctly.
	run := strings.Repeat("a", 5000)
	encRun := slz.Compress([]byte(run))
	decRun, err := slz.Decompress(encRun, len(run))
	if err != nil || string(decRun) != run {
		fmt.Println("run decode error:", err)
		os.Exit(1)
	}
	fmt.Printf("run of 5000 'a': %d bytes, round trip: true\n", len(encRun))
}

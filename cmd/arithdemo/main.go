// Command arithdemo compresses a binary stream (90% zeros) with arithmetic
// coding and compares the size against the Shannon entropy and the Huffman
// lower bound.
package main

import (
	"fmt"
	"math"
	"math/rand"

	"arithmetic"
)

func main() {
	const n = 100000
	const p0 = 0.9

	rng := rand.New(rand.NewSource(7))
	symbols := make([]int, n)
	zeros := 0
	for i := range symbols {
		if rng.Float64() < p0 {
			symbols[i] = 0
			zeros++
		} else {
			symbols[i] = 1
		}
	}

	encoded := arithmetic.Encode(symbols, arithmetic.NewBinaryModel())
	decoded := arithmetic.Decode(encoded, arithmetic.NewBinaryModel())

	ok := len(decoded) == n
	for i := range symbols {
		if decoded[i] != symbols[i] {
			ok = false
			break
		}
	}

	entropy := -(p0*math.Log2(p0) + (1-p0)*math.Log2(1-p0))
	fmt.Printf("symbols:              %d (zeros: %d, %.1f%%)\n", n, zeros, 100*float64(zeros)/n)
	fmt.Printf("arithmetic coded:     %d bytes (%.4f bits/symbol)\n", len(encoded), 8*float64(len(encoded))/n)
	fmt.Printf("shannon entropy:      %.0f bytes (%.4f bits/symbol)\n", entropy*n/8, entropy)
	fmt.Printf("huffman lower bound:  %d bytes (1.0000 bits/symbol)\n", n/8)
	fmt.Printf("vs huffman:           %.1f%% smaller\n", 100*(1-float64(len(encoded))/(n/8)))
	fmt.Printf("round-trip lossless:  %v\n", ok)
}

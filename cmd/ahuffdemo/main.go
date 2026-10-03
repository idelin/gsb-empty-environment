// Command ahuffdemo demonstrates the FGK adaptive Huffman codec on a
// long, heavily skewed text stream: it encodes in a single streaming
// pass, reports the compression ratio, decodes the stream back, and
// verifies byte-for-byte equality with the original.
package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"

	ah "adaptivehuffman"
)

// skewedText builds a deterministic text whose byte frequencies decay
// geometrically, mimicking natural-language skew.
func skewedText(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	const alphabet = "etaoinshrdlucmfwypvbgkqjxzETAOINSHRDLU0123456789 .,;:!?-\n"
	weights := make([]int, len(alphabet))
	total := 0
	for i := range weights {
		weights[i] = 1 << max(0, 12-i/3)
		total += weights[i]
	}
	out := make([]byte, n)
	for i := range out {
		x := r.Intn(total)
		for j, w := range weights {
			if x < w {
				out[i] = alphabet[j]
				break
			}
			x -= w
		}
	}
	return out
}

func main() {
	const n = 4 << 20 // 4 MiB
	data := skewedText(n, 2024)
	fmt.Printf("input:            %d bytes (single-pass stream, skewed distribution)\n", len(data))

	// Encode: one streaming pass, no preliminary frequency scan.
	var compressed bytes.Buffer
	enc := ah.NewEncoder(&compressed)
	const chunk = 64 * 1024
	for off := 0; off < len(data); off += chunk {
		end := min(off+chunk, len(data))
		if _, err := enc.Write(data[off:end]); err != nil {
			panic(err)
		}
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}
	fmt.Printf("compressed:       %d bytes (ratio %.3f, %.1f bits/byte)\n",
		compressed.Len(),
		float64(compressed.Len())/float64(len(data)),
		8*float64(compressed.Len())/float64(len(data)))
	fmt.Printf("tree nodes:       %d (bounded by 2*256+1 = 513 => O(1) memory)\n", enc.TreeSize())

	// Decode: the receiver rebuilds the identical tree on the fly.
	dec := ah.NewDecoder(bytes.NewReader(compressed.Bytes()))
	plain, err := io.ReadAll(dec)
	if err != nil {
		panic(err)
	}
	fmt.Printf("decoded:          %d bytes, decoder tree nodes: %d\n", len(plain), dec.TreeSize())
	if bytes.Equal(plain, data) {
		fmt.Println("verification:     OK - decoded output is byte-identical to the input")
	} else {
		panic("decoded output differs from the input")
	}
}

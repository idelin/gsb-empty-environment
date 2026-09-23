// Command slzdemo compresses a sample text with slz, reports sizes,
// and verifies the roundtrip.
package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"slz"
)

func main() {
	sample := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 40)
	sample += strings.Repeat("a", 300) // distance-1 run extension
	sample += sample[:500]             // far back-reference

	var buf bytes.Buffer
	if err := slz.Encode(&buf, []byte(sample), 1024); err != nil {
		log.Fatal(err)
	}
	encLen := buf.Len()
	dec, err := slz.Decode(&buf, 1<<20)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("original:  %d bytes\n", len(sample))
	fmt.Printf("encoded:   %d bytes (%.1f%% of original)\n",
		encLen, 100*float64(encLen)/float64(len(sample)))
	fmt.Printf("roundtrip: %v\n", bytes.Equal(dec, []byte(sample)))
}

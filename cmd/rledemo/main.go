// Command rledemo compresses a sample input offline and online, prints
// sizes and the encoded stream, and verifies decoding.
package main

import (
	"bytes"
	"fmt"
	"strings"

	"rlecomp"
)

func main() {
	sample := "header: " + strings.Repeat("ab", 6) +
		strings.Repeat("Z", 300) +
		" middle " + strings.Repeat("Q", 7) + " tail"
	src := []byte(sample)

	// Offline optimal.
	offline := rlecomp.Compress(src)
	fmt.Printf("input:            %d bytes\n", len(src))
	fmt.Printf("offline optimal:  %d bytes (%d frames)\n", len(offline), mustFrames(offline))
	fmt.Printf("stream hex:       % x\n", offline)

	// Online with a small bounded buffer and explicit flushes.
	var buf bytes.Buffer
	enc := rlecomp.NewEncoder(&buf, 64)
	half := len(src) / 2
	write(enc, src[:half])
	must(enc.Flush()) // explicit boundary, stream continues
	write(enc, src[half:])
	must(enc.Close())
	online := buf.Bytes()
	fmt.Printf("online (w=64, 1 flush): %d bytes (%d frames)\n", len(online), mustFrames(online))

	// Documented online gap bound.
	bound := rlecomp.WorstCaseBound(len(src), 64, 1)
	fmt.Printf("online gap bound: %d bytes total (actual %d)\n", bound, len(online))

	// Decode with a bounded output quota.
	dec, frames, err := rlecomp.DecodeAll(bytes.NewReader(online), 1<<20)
	must(err)
	fmt.Printf("decoded:          %d bytes in %d frames, matches input: %v\n",
		len(dec), frames, bytes.Equal(dec, src))

	// Empty input: one empty frame, distinct from no frame.
	empty := rlecomp.Compress(nil)
	fmt.Printf("empty input:      %d bytes, frames=%d (no frame = 0 bytes)\n",
		len(empty), mustFrames(empty))
}

func mustFrames(stream []byte) int {
	_, frames, err := rlecomp.DecodeAll(bytes.NewReader(stream), 1<<30)
	must(err)
	return frames
}

func write(enc *rlecomp.Encoder, p []byte) {
	if _, err := enc.Write(p); err != nil {
		panic(err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

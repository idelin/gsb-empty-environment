// Command huffdemo encodes a sample text with huffcodec, prints the
// deterministic canonical code table, Kraft sum and optimal cost, then
// decodes the stream in small chunks to demonstrate streaming recovery.
package main

import (
	"bytes"
	"fmt"
	"os"

	"huffcodec"
)

func main() {
	sample := []byte("the quick brown fox jumps over the lazy dog. " +
		"the quick brown fox jumps over the lazy dog, again and again!")
	const maxBits = 16

	var freqs [256]uint64
	for _, b := range sample {
		freqs[b]++
	}
	lengths, err := huffcodec.CodeLengths(freqs[:], maxBits)
	if err != nil {
		fmt.Fprintln(os.Stderr, "code lengths:", err)
		os.Exit(1)
	}
	codes, err := huffcodec.CanonicalCodes(lengths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "canonical codes:", err)
		os.Exit(1)
	}

	fmt.Printf("input: %q\n", sample)
	fmt.Printf("symbols: %d distinct, %d bytes, max code length %d\n\n",
		countUsed(lengths), len(sample), maxBits)
	fmt.Println("sym  char  freq  len  code")
	for sym, l := range lengths {
		if l == 0 {
			continue
		}
		fmt.Printf("%3d  %4q  %4d  %4d  %0*b\n", sym, sym, freqs[sym], l, int(l), codes[sym].Bits)
	}

	kraft := huffcodec.KraftScaled(lengths)
	fmt.Printf("\nKraft sum: %d/2^32 = %.6f (1.0 = complete)\n",
		kraft, float64(kraft)/(1<<huffcodec.MaxCodeLength))
	cost := huffcodec.Cost(freqs[:], lengths)
	fmt.Printf("optimal cost: %d bits payload (%.2f bits/symbol)\n",
		cost, float64(cost)/float64(len(sample)))

	enc, err := huffcodec.Encode(sample, maxBits)
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
	fmt.Printf("wire size: %d bytes (header + payload), ratio %.3f\n\n",
		len(enc), float64(len(enc))/float64(len(sample)))

	// Whole-buffer decode.
	dec, err := huffcodec.DecodeAll(enc, 1<<20)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decode-all:", err)
		os.Exit(1)
	}
	fmt.Printf("DecodeAll round-trip: %v\n", bytes.Equal(dec, sample))

	// Streaming decode of the payload in 5-byte chunks with a tiny
	// output buffer, proving cross-byte/cross-call state handling.
	hdr := headerLen(enc)
	sd, err := huffcodec.NewStreamDecoder(lengths, int64(len(sample)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "stream decoder:", err)
		os.Exit(1)
	}
	payload := enc[hdr:]
	var got []byte
	buf := make([]byte, 7)
	for off := 0; off < len(payload); {
		n := 5
		if off+n > len(payload) {
			n = len(payload) - off
		}
		c, p, err := sd.Decode(payload[off:off+n], buf)
		if err != nil && sd.Produced < int64(len(sample)) {
			fmt.Fprintln(os.Stderr, "stream decode:", err)
			os.Exit(1)
		}
		got = append(got, buf[:p]...)
		off += c
	}
	for sd.Produced < int64(len(sample)) {
		_, p, _ := sd.Decode(nil, buf)
		if p == 0 {
			break
		}
		got = append(got, buf[:p]...)
	}
	fmt.Printf("streamed round-trip (5-byte chunks): %v\n", bytes.Equal(got, sample))
}

func countUsed(lengths []uint8) int {
	n := 0
	for _, l := range lengths {
		if l > 0 {
			n++
		}
	}
	return n
}

// headerLen walks the wire header to find the payload offset (demo only).
func headerLen(enc []byte) int {
	i := 3
	nSym, n := uvarint(enc[i:])
	i += n + int(nSym)*2
	_, n2 := uvarint(enc[i:])
	i += n2 + 4
	return i
}

func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i, c := range b {
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

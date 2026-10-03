// Demo: Huffman error-tolerant decoding with sync words over an unreliable
// channel. It encodes a message, erases 3 bits in the middle, then compares
// an ordinary Huffman decoder against the sync-protected resilient decoder.
package main

import (
	"fmt"

	"huffsync"
)

func sampleMessage() []byte {
	base := "The quick brown fox jumps over the lazy dog. " +
		"Pack my box with five dozen liquor jugs. " +
		"How vexingly quick daft zebras jump! " +
		"Bright vixens jump; dozy fowl quack. "
	var buf []byte
	for len(buf) < 4000 {
		buf = append(buf, base...)
	}
	return buf[:4000]
}

func commonPrefix(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func snippet(b []byte, start, n int) string {
	if start >= len(b) {
		return ""
	}
	end := start + n
	if end > len(b) {
		end = len(b)
	}
	return string(b[start:end])
}

func main() {
	msg := sampleMessage()
	var freq [huffsync.NumSymbols]uint64
	for _, s := range msg {
		freq[s]++
	}
	freq[huffsync.SymInvalid] = 1
	code := huffsync.BuildHuffman(freq)

	const eraseBit = 50 * 8 // erase 3 bits starting at byte 50

	fmt.Printf("message:            %d bytes\n", len(msg))
	fmt.Printf("channel damage:     erased 3 bits at byte 50\n\n")

	// --- Ordinary Huffman stream: no sync words, no recovery ---
	plain := huffsync.EncodePlain(msg, code)
	plainOut := huffsync.DecodePlain(huffsync.DeleteBits(plain, eraseBit, 3), code)
	prefix := commonPrefix(plainOut, msg)
	fmt.Printf("[ordinary decoder]  %d bytes encoded, no framing\n", plain.Len()/8)
	fmt.Printf("  correct symbols before losing phase: %d / %d\n", prefix, len(msg))
	fmt.Printf("  everything after the erasure is garbage, e.g.:\n")
	fmt.Printf("  want: %q\n", snippet(msg, prefix, 48))
	fmt.Printf("  got:  %q\n\n", snippet(plainOut, prefix, 48))

	// --- Resilient stream: sync word every 100 bytes of wire format ---
	enc := huffsync.EncodeResilient(msg, code)
	damaged := huffsync.DeleteBits(enc.Bits, eraseBit, 3)
	res := huffsync.DecodeResilient(damaged, code)

	fmt.Printf("[resilient decoder] %d frames of %d bytes (32-bit sync word + %d-byte segment)\n",
		len(res.Segments), huffsync.FrameBytes, huffsync.SegmentBytes)
	fmt.Printf("  segment 0 absorbed the damage and was truncated after %d/%d symbols\n",
		commonPrefix(res.Segments[0].Symbols, msg[:enc.SymbolsPerSegment[0]]),
		enc.SymbolsPerSegment[0])
	syncByte := res.Segments[1].SyncBitPos / 8
	fmt.Printf("  re-locked at sync word originally at byte 100 (found at bit %d, ~byte %d, of the damaged stream)\n",
		res.Segments[1].SyncBitPos, syncByte)

	correct, off := 0, 0
	for i, cnt := range enc.SymbolsPerSegment {
		want := msg[off : off+cnt]
		if i == 0 {
			correct += commonPrefix(res.Segments[i].Symbols, want)
		} else if string(res.Segments[i].Symbols) == string(want) {
			correct += cnt
		}
		off += cnt
	}
	fmt.Printf("  salvaged %d / %d symbols (%.2f%%)\n", correct, len(msg), 100*float64(correct)/float64(len(msg)))
	fmt.Printf("  content after the byte-100 sync word is intact, e.g.:\n")
	fmt.Printf("  %q\n", snippet(res.Output, len(res.Segments[0].Symbols), 64))
}

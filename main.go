package main

import (
	"bytes"
	"fmt"
	"strings"
)

func buildSampleText() string {
	sentence := "Huffman coding assigns short codes to frequent symbols; " +
		"a resilient decoder inserts sync words so it can re-lock " +
		"at the next marker after a bit error on the channel. "
	var b strings.Builder
	for b.Len() < 2000 {
		b.WriteString(sentence)
	}
	return b.String()
}

func printable(b []byte) string {
	s := string(b)
	if len(s) > 72 {
		s = s[:72] + "..."
	}
	return fmt.Sprintf("%q", s)
}

func main() {
	data := []byte(buildSampleText())
	codes := buildCodes(data)
	enc := EncodeFrames(data, codes)

	fmt.Println("=== Huffman fault-tolerant decoder demo ===")
	fmt.Printf("source text : %d bytes, %d distinct symbols\n", len(data), len(codes))
	fmt.Printf("encoded     : %d frames x %d B = %d B (sync word every %d B, at bytes 0, 50, 100, ...)\n",
		len(enc.FrameSymbols), FrameBytes, len(enc.Data), FrameBytes)

	// Corrupt the stream: erase 3 bits at byte 60 (inside frame #1's payload).
	const corruptByte = 60
	corrupted := DeleteBits(enc.Data, corruptByte*8, 3)
	fmt.Printf("corruption  : deleted 3 bits at byte %d (bits %d-%d)\n",
		corruptByte, corruptByte*8, corruptByte*8+2)

	corruptSym := enc.FrameStartSym[1] // first symbol at/after the damage
	postCorrupt := enc.SymbolsFromFrame(1)

	fmt.Println("\n--- naive decoder (no resync) ---")
	naive := DecodeNaive(corrupted, codes)
	fmt.Printf("prefix OK   : %s\n", printable(naive[:corruptSym]))
	fmt.Printf("then garbage: %s\n", printable(naive[corruptSym:corruptSym+80]))
	fmt.Printf("post-corruption content match: %.1f%%\n",
		100*positionMatch(naive[corruptSym:], data[corruptSym:]))

	fmt.Println("\n--- fault-tolerant decoder ---")
	ft, events := DecodeFaultTolerant(corrupted, codes)
	for _, e := range events {
		note := ""
		if e.Kind == EventResync {
			note = fmt.Sprintf("  <-- sync word written at byte %d (shifted to bit %d by the 3 deleted bits)",
				(e.BitPos+3)/8, e.BitPos)
		}
		fmt.Printf("  bit %5d  %-10s %s%s\n", e.BitPos, e.Kind, e.Msg, note)
	}
	tail := data[enc.FrameStartSym[2]:]
	intact := bytes.HasSuffix(ft, tail)
	salvage := float64(enc.SymbolsFromFrame(2)) / float64(postCorrupt)
	fmt.Printf("resynced at the sync word of byte 100; everything from frame #2 on is intact: %v\n", intact)
	fmt.Printf("salvaged    : %d of %d post-corruption symbols = %.1f%%\n",
		enc.SymbolsFromFrame(2), postCorrupt, 100*salvage)
	fmt.Printf("recovered tail: %s\n", printable(ft[len(ft)-80:]))
}

// Command mtfdemo demonstrates the MTF transform: index encoding, the
// escape protocol for unknown symbols, block-boundary checkpoints and
// bounded decoding.
package main

import (
	"encoding/hex"
	"fmt"
	"log"

	"mtf"
)

func main() {
	// Part 1: plain index encoding. Repeated symbols encode as 0.
	alpha, err := mtf.NewAlphabet([]byte("abcdef"))
	if err != nil {
		log.Fatal(err)
	}
	m := mtf.NewModel(alpha)
	fmt.Println("== Part 1: index encoding (alphabet \"abcdef\") ==")
	for _, sym := range []byte("ddcab") {
		idx, err := m.EncodeIndex(sym)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  encode %q -> index %d   state %q\n", sym, idx, m.Order())
	}

	// Part 2: escape protocol + checkpoint bound to the block boundary.
	fmt.Println("\n== Part 2: escape protocol + checkpoint/restore ==")
	encAlpha, _ := mtf.NewAlphabet([]byte("abcdef"))
	encModel := mtf.NewModel(encAlpha)
	checkpoint := encModel.Checkpoint() // block boundary
	fmt.Printf("  checkpoint: %d bytes  %s\n", len(checkpoint), hex.EncodeToString(checkpoint))

	input := []byte("cabxdy") // 'x' and 'y' are unknown -> explicit escapes
	enc := mtf.NewBlockEncoder(encModel, mtf.EscapeUnknown)
	for _, sym := range input {
		if err := enc.Write(sym); err != nil {
			log.Fatal(err)
		}
	}
	stream := enc.Bytes()
	fmt.Printf("  input:     %q\n  stream:    %s\n", input, hex.EncodeToString(stream))
	fmt.Printf("  alphabet:  version %d, size %d (extended via escapes)\n",
		encModel.Alphabet().Version(), encModel.Alphabet().Size())

	// Decoder restores the block-start state from the checkpoint only.
	decAlpha, _ := mtf.NewAlphabet([]byte("abcdef"))
	decModel, err := mtf.Restore(decAlpha, checkpoint)
	if err != nil {
		log.Fatal(err)
	}
	out, err := mtf.NewBlockDecoder(decModel).Decode(stream, 1<<16)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  decoded:   %q (match=%v)\n", out, string(out) == string(input))
	fmt.Printf("  decoder alphabet version %d, states in sync: %v\n",
		decModel.Alphabet().Version(),
		string(encModel.Order()) == string(decModel.Order()))
}

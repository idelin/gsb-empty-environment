// Command mtfdemo demonstrates the MTF transform: versioned alphabets, the
// escape-update protocol for unknown symbols, block-boundary checkpoints,
// illegal-index rejection, and the checksummed block format.
package main

import (
	"fmt"
	"log"

	"mtf"
)

func main() {
	alpha, err := mtf.NewAlphabet([]rune("abcdefghij"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("alphabet v%d: %q\n", alpha.Version(), string(alpha.Symbols()))

	ext, err := alpha.Extend('k', 'l')
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("extended  v%d: %q\n", ext.Version(), string(ext.Symbols()))
	if _, err := ext.Extend('a'); err != nil {
		fmt.Printf("duplicate extension rejected: %v\n", err)
	}

	enc := mtf.NewEncoder(alpha, mtf.PolicyEscapeUpdate)
	dec := mtf.NewDecoder(alpha)
	input := "abacabadaz"
	var toks []mtf.Token
	fmt.Print("encode:")
	for _, s := range input {
		ts, err := enc.Encode(s)
		if err != nil {
			log.Fatal(err)
		}
		for _, tok := range ts {
			if tok.Literal {
				fmt.Printf(" <%c>", tok.Sym)
			} else {
				fmt.Printf(" %d", tok.Index)
			}
		}
		toks = append(toks, ts...)
	}
	fmt.Println()

	out, err := dec.DecodeStream(toks)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("decode: %q (match=%v, alphabet v%d on both sides)\n",
		string(out), string(out) == input, dec.Model().Alphabet().Version())

	enc2 := mtf.NewEncoder(alpha, mtf.PolicyEscapeUpdate)
	var block1, block2 []mtf.Token
	for _, s := range "cafe" {
		ts, _ := enc2.Encode(s)
		block1 = append(block1, ts...)
	}
	cp := enc2.Model().Checkpoint()
	for _, s := range "babe" {
		ts, _ := enc2.Encode(s)
		block2 = append(block2, ts...)
	}
	enc2.Model().Restore(cp)
	var replay []mtf.Token
	for _, s := range "babe" {
		ts, _ := enc2.Encode(s)
		replay = append(replay, ts...)
	}
	_ = block1
	fmt.Printf("checkpoint replay identical: %v\n", fmt.Sprint(block2) == fmt.Sprint(replay))

	before := fmt.Sprint(dec.Model().Order())
	if _, err := dec.Model().Decode(999); err != nil {
		fmt.Printf("index 999 rejected: %v (state unchanged=%v)\n",
			err, before == fmt.Sprint(dec.Model().Order()))
	}

	blob, err := mtf.EncodeBlock(alpha.Version(), toks)
	if err != nil {
		log.Fatal(err)
	}
	dec2 := mtf.NewDecoder(alpha)
	got, err := dec2.DecodeBlock(blob, 1024)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("block: %d bytes, decoded %q with quota 1024\n", len(blob), string(got))
	if _, err := mtf.NewDecoder(alpha).DecodeBlock(blob, 3); err != nil {
		fmt.Printf("quota 3 enforced: %v\n", err)
	}
	blob[len(blob)-1] ^= 0xff
	if _, err := mtf.NewDecoder(alpha).DecodeBlock(blob, 1024); err != nil {
		fmt.Printf("corrupted block rejected: %v\n", err)
	}
}

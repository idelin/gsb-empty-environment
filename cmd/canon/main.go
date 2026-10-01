// Command canon demonstrates canonical encoding of a document and of a
// rooted graph with shared references and a cycle.
package main

import (
	"bytes"
	"fmt"
	"log"

	"canon"
)

func main() {
	// A document: object key order in the source is irrelevant.
	doc := canon.Object(
		canon.Pair{Key: "name", Value: canon.String("demo")},
		canon.Pair{Key: "version", Value: canon.Int(1)},
		canon.Pair{Key: "tags", Value: canon.Array(canon.String("a"), canon.String("b"))},
		canon.Pair{Key: "ratio", Value: canon.Float(2.5)},
		canon.Pair{Key: "note", Value: canon.Null()},
	)
	docEnc, err := canon.Encode(doc)
	if err != nil {
		log.Fatal(err)
	}
	docDigest, err := canon.Digest(doc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("document encoding (%d bytes): %x\n", len(docEnc), docEnc)
	fmt.Printf("document sha256 digest:       %x\n", docDigest)

	// A rooted graph with a shared reference (c is shared by a and b)
	// and a cycle (c points back to a).
	a := &canon.Node{Label: canon.String("a")}
	b := &canon.Node{Label: canon.String("b")}
	c := &canon.Node{Label: canon.String("c")}
	a.Edges = []*canon.Node{b, c}
	b.Edges = []*canon.Node{c}
	c.Edges = []*canon.Node{a}

	g1, err := canon.Encode(canon.GraphValue(a))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("graph encoding (%d bytes):    %x\n", len(g1), g1)

	// The same labeled graph, built in a different creation order.
	c2 := &canon.Node{Label: canon.String("c")}
	b2 := &canon.Node{Label: canon.String("b")}
	a2 := &canon.Node{Label: canon.String("a")}
	c2.Edges = []*canon.Node{a2}
	b2.Edges = []*canon.Node{c2}
	a2.Edges = []*canon.Node{b2, c2}

	g2, err := canon.Encode(canon.GraphValue(a2))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("reordered build identical:    %v\n", bytes.Equal(g1, g2))

	// Decode and re-encode: canonical form is a fixed point.
	dec, err := canon.Decode(g1)
	if err != nil {
		log.Fatal(err)
	}
	re, err := canon.Encode(dec)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("decode/re-encode identical:   %v\n", bytes.Equal(g1, re))
}

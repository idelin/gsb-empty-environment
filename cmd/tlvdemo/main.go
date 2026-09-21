// Command tlvdemo demonstrates the tlv package: chunked streaming
// decode, unknown-type passthrough, nesting, canonical encoding and
// boundary/quota error reporting.
package main

import (
	"errors"
	"fmt"
	"math/rand"

	"example.com/tlv"
)

func dump(fields []*tlv.Field, indent string) {
	for _, f := range fields {
		if tlv.IsContainer(f.Type) {
			fmt.Printf("%stype 0x%04x container (%d children)\n", indent, f.Type, len(f.Children))
			dump(f.Children, indent+"  ")
		} else {
			fmt.Printf("%stype 0x%04x value %x\n", indent, f.Type, f.Value)
		}
	}
}

func main() {
	doc := []*tlv.Field{
		tlv.NewPrimitive(0x0001, []byte("hello")),
		tlv.NewContainer(0x0002,
			tlv.NewPrimitive(0x0003, []byte{0xde, 0xad}),
			tlv.NewContainer(0x0004), // empty container is legal
		),
		tlv.NewPrimitive(0x4321, []byte{0x01, 0x02, 0x03}), // unknown type
		tlv.NewPrimitive(0x0001, []byte("world")),          // duplicate type
	}
	wire := tlv.Encode(doc)
	fmt.Printf("wire (%d bytes): %x\n\n", len(wire), wire)

	// Stream the document in random 1..7-byte chunks.
	p := tlv.NewParser()
	rng := rand.New(rand.NewSource(7))
	for rest := wire; len(rest) > 0; {
		n := 1 + rng.Intn(7)
		if n > len(rest) {
			n = len(rest)
		}
		if err := p.Feed(rest[:n]); err != nil {
			panic(err)
		}
		rest = rest[n:]
	}
	fields, err := p.Finalize()
	if err != nil {
		panic(err)
	}
	fmt.Println("streamed decode (random chunks):")
	dump(fields, "  ")

	// Unknown types and duplicates survive a decode/re-encode cycle.
	if re := tlv.Encode(fields); string(re) == string(wire) {
		fmt.Println("\nre-encode: byte-identical (unknown types preserved)")
	}

	fmt.Printf("\ncanonical:  %x\n", tlv.EncodeCanonical(doc))

	// Truncated input is reported as "need more data", not corruption.
	if _, err := tlv.Parse(wire[:len(wire)-1]); errors.Is(err, tlv.ErrNeedMoreData) {
		fmt.Printf("\ntruncated input: %v\n", err)
	}

	// A child claiming bytes past its parent boundary reports the
	// child's absolute stream offset.
	bad := []byte{
		0x00, 0x01, 0, 0, 0, 2, 0xaa, 0xaa, // offset 0: padding
		0x80, 0x02, 0, 0, 0, 9, // offset 8: container, 9-byte value
		0x00, 0x03, 0, 0, 0, 10, 0x01, 0x02, 0x03, // offset 14: greedy child
	}
	if _, err := tlv.Parse(bad); err != nil {
		fmt.Printf("boundary violation: %v\n", err)
	}

	// Length quota is enforced before allocation, from the header alone.
	qp := &tlv.Parser{MaxValueLen: 8}
	if err := qp.Feed([]byte{0x00, 0x01, 0x40, 0x00, 0x00, 0x00}); err != nil {
		fmt.Printf("quota exceeded:   %v\n", err)
	}
}

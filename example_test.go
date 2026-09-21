package tlv_test

import (
	"errors"
	"fmt"

	"tlv"
)

// Example builds a nested message containing an unknown field, encodes it
// canonically, feeds it back one byte at a time, and shows that unknown
// fields survive decode/encode byte-identically.
func Example() {
	msg := &tlv.Value{Type: tlv.ContainerFlag | 0x01, Children: []*tlv.Value{
		{Type: 1, Raw: []byte("alice")},                 // known: name
		{Type: 0x9999, Raw: []byte{0xDE, 0xAD}},         // unknown: passthrough
		{Type: tlv.ContainerFlag | 0x02, Children: nil}, // empty container
	}}
	wire := msg.Encode()
	fmt.Printf("wire: %x\n", wire)

	dec := &tlv.Decoder{}
	var got *tlv.Value
	for i, b := range wire {
		dec.Write([]byte{b})
		v, err := dec.Next()
		if errors.Is(err, tlv.ErrIncomplete) {
			continue
		}
		if err != nil {
			fmt.Println("corrupt:", err)
			return
		}
		got = v
		_ = i
	}
	fmt.Printf("children: %d, unknown field: %x\n",
		len(got.Children), got.Children[1].Raw)
	fmt.Println("round-trip identical:", string(got.Encode()) == string(wire))

	// Output:
	// wire: 800000010000001f0000000100000005616c6963650000999900000002dead8000000200000000
	// children: 3, unknown field: dead
	// round-trip identical: true
}

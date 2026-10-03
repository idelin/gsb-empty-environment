package codec_test

import (
	"fmt"

	"pbcodec/codec"
)

type Point struct {
	X codec.SInt32 `pb:"1"` // zigzag varint
	Y codec.SInt32 `pb:"2"`
}

type Shape struct {
	Name   string   `pb:"1"`
	Points []*Point `pb:"2"` // nested length-delimited messages
	Ids    []uint64 `pb:"3"` // packed repeated
}

func Example() {
	in := &Shape{
		Name:   "triangle",
		Points: []*Point{{X: -1, Y: 2}, {X: 3, Y: -4}},
		Ids:    []uint64{7, 8, 9},
	}
	enc, err := codec.Marshal(in)
	if err != nil {
		panic(err)
	}
	fmt.Printf("encoded %d bytes: %x\n", len(enc), enc)

	var out Shape
	if err := codec.Unmarshal(enc, &out); err != nil {
		panic(err)
	}
	fmt.Println(out.Name, out.Points[0], out.Points[1], out.Ids)

	// Truncated input is reported, never panics.
	err = codec.Unmarshal(enc[:len(enc)-2], &out)
	fmt.Println("truncated:", err)
	// Output:
	// encoded 27 bytes: 0a08747269616e676c651204080110041204080610071a03070809
	// triangle &{-1 2} &{3 -4} [7 8 9]
	// truncated: codec: field 3: wire: truncated input
}

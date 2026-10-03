// Command pbdemo demonstrates the zero-dependency protobuf wire-format
// codec: compact binary encoding, zero-copy decoding, forward/backward
// compatibility and truncation safety.
package main

import (
	"encoding/hex"
	"fmt"
	"reflect"

	"pbcodec/codec"
)

type Geo struct {
	Lat codec.SFixed64 `pb:"1"`
	Lng codec.SFixed64 `pb:"2"`
}

type Address struct {
	City string `pb:"1"`
	Geo  *Geo   `pb:"2"`
}

type Person struct {
	codec.UnknownFields
	Name    string        `pb:"1"`
	Age     int32         `pb:"2"`
	Scores  []int32       `pb:"3"` // packed repeated
	Addr    *Address      `pb:"4"` // nested message (2 levels)
	Balance codec.SInt64  `pb:"5"` // zigzag
	ID      codec.Fixed64 `pb:"6"`
}

// PersonV1 models an old client that only knows fields 1-2.
type PersonV1 struct {
	codec.UnknownFields
	Name string `pb:"1"`
	Age  int32  `pb:"2"`
}

func main() {
	in := &Person{
		Name:    "Ada",
		Age:     36,
		Scores:  []int32{1, -2, 300},
		Addr:    &Address{City: "London", Geo: &Geo{Lat: 515283750, Lng: -1417956}},
		Balance: -42,
		ID:      0xdeadbeef,
	}

	enc, err := codec.Marshal(in)
	must(err)
	fmt.Printf("1. compact encoding (%d bytes):\n   %s\n", len(enc), hex.Dump(enc))

	var out Person
	must(codec.Unmarshal(enc, &out))
	fmt.Printf("2. zero-copy decode: %+v addr=%+v geo=%+v\n", out.Name, *out.Addr, *out.Addr.Geo)
	fmt.Println("   deep equal:", reflect.DeepEqual(in, &out))

	var old PersonV1
	must(codec.Unmarshal(enc, &old))
	fmt.Printf("3. old schema reads new data: name=%q age=%d, %d unknown bytes preserved\n",
		old.Name, old.Age, len(old.UnknownFields.Raw))
	reenc, err := codec.Marshal(&old)
	must(err)
	var relay Person
	must(codec.Unmarshal(reenc, &relay))
	fmt.Println("   lossless relay through old schema:", reflect.DeepEqual(in, &relay))

	cut := enc[:len(enc)-3]
	err = codec.Unmarshal(cut, &out)
	fmt.Printf("4. truncated stream (%d/%d bytes) safely rejected: %v\n", len(cut), len(enc), err)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

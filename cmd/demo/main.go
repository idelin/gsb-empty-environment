// Demo: decode with schema v1, mutate a known field, upgrade to schema v2,
// and show that unknown segments survive byte-for-byte.
package main

import (
	"fmt"

	"wiremsg"
)

func varint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func field(num uint32, wt int, payload []byte) []byte {
	out := varint(uint64(num)<<3 | uint64(wt))
	return append(out, payload...)
}

func main() {
	v1 := &wiremsg.Schema{Fields: map[uint32]wiremsg.FieldSchema{
		1: {Kind: wiremsg.KindVarint},
		2: {Kind: wiremsg.KindString},
	}}
	v2 := &wiremsg.Schema{Fields: map[uint32]wiremsg.FieldSchema{
		1: {Kind: wiremsg.KindVarint},
		2: {Kind: wiremsg.KindString},
		3: {Kind: wiremsg.KindVarint}, // unknown under v1, known under v2
	}}

	// Wire input: known varint, unknown field 9, known string, unknown field 3.
	input := append([]byte{},
		field(1, wiremsg.WireVarint, varint(1))...)
	input = append(input, field(9, wiremsg.WireBytes, append(varint(3), 0xAA, 0xBB, 0xCC))...)
	input = append(input, field(2, wiremsg.WireBytes, append(varint(2), 'h', 'i'))...)
	input = append(input, field(3, wiremsg.WireVarint, varint(77))...)
	fmt.Printf("input            : % x\n", input)

	msg, usage, err := wiremsg.Decode(input, v1, wiremsg.Budget{MaxFields: 64, MaxDepth: 8})
	if err != nil {
		panic(err)
	}
	fmt.Printf("decoded          : fields=%d depth=%d valueBytes=%d\n",
		usage.Fields, usage.Depth, usage.ValueBytes)
	for i, s := range msg.Segments() {
		if s.Known {
			fmt.Printf("  seg %d: known  num=%d value=%+v\n", i, s.Num, s.Value)
		} else {
			fmt.Printf("  seg %d: unknown num=%d raw=% x (shared with input)\n", i, s.Num, s.Raw)
		}
	}

	// Grow known field 1: 1-byte varint -> 3-byte varint. Unknown offsets shift.
	grown, err := msg.Set(1, wiremsg.Value{Kind: wiremsg.KindVarint, U64: 0x4000})
	if err != nil {
		panic(err)
	}
	fmt.Printf("after Set(1)     : % x\n", grown.Encode())
	fmt.Printf("offsets before   : %v\n", msg.Offsets())
	fmt.Printf("offsets after    : %v (recomputed)\n", grown.Offsets())

	// Schema upgrade: field 3 becomes known and is parsed from its raw bytes.
	up, err := grown.Upgrade(v2, wiremsg.Budget{})
	if err != nil {
		panic(err)
	}
	if v, ok := up.Get(3); ok {
		fmt.Printf("after upgrade    : field 3 is now known, value=%d\n", v.U64)
	}
	fmt.Printf("upgraded bytes   : % x (identical to input except field 1)\n", up.Encode())

	// Failure atomicity: a conflicting upgrade leaves the original untouched.
	conflict := append([]byte{}, input...)
	conflict = append(conflict, field(3, wiremsg.WireVarint, varint(88))...)
	cm, _, _ := wiremsg.Decode(conflict, v1, wiremsg.Budget{})
	before := cm.Encode()
	if _, err := cm.Upgrade(v2, wiremsg.Budget{}); err != nil {
		fmt.Printf("conflict upgrade : rejected: %v\n", err)
	}
	fmt.Printf("original intact  : %v\n", string(before) == string(cm.Encode()))
}

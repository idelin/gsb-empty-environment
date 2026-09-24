// Demo: parse with an old schema, upgrade the schema, mutate a known field,
// and show that unknown bytes survive byte-for-byte.
package main

import (
	"fmt"

	"example.com/wiremsg/wiremsg"
)

func main() {
	// Wire bytes produced by a newer writer. Field 7 (repeated) and field 9
	// are unknown to schema v1.
	wire := []byte{
		0x08, 0x96, 0x01, // field 1 varint 150
		0x12, 0x02, 'h', 'i', // field 2 string "hi"
		0x38, 0x01, // field 7 varint 1
		0x38, 0x02, // field 7 varint 2 (repeat)
		0x4a, 0x02, 0xff, 0x00, // field 9 raw bytes
	}
	fmt.Printf("input wire bytes: % x\n\n", wire)

	v1 := wiremsg.NewSchema(map[uint32]wiremsg.FieldDef{
		1: {Name: "count", Type: wiremsg.TypeUint64},
		2: {Name: "name", Type: wiremsg.TypeString},
	})
	v2 := wiremsg.NewSchema(map[uint32]wiremsg.FieldDef{
		1: {Name: "count", Type: wiremsg.TypeUint64},
		2: {Name: "name", Type: wiremsg.TypeString},
		7: {Name: "flag", Type: wiremsg.TypeUint64, Repeated: true},
	})

	budget := wiremsg.NewBudget(wiremsg.DefaultLimits)
	m, err := wiremsg.Parse(v1, wire, budget)
	if err != nil {
		panic(err)
	}
	usedB, usedF := budget.Used()
	fmt.Printf("parsed with schema v1: %d fields, budget used %d bytes / %d fields\n",
		len(m.Fields), usedB, usedF)
	for _, f := range m.Fields {
		if f.Known {
			fmt.Printf("  known  field %d (%s)\n", f.Number, f.Def.Name)
		} else {
			fmt.Printf("  unknown field %d raw=% x (shared with input)\n", f.Number, f.Raw)
		}
	}

	// Mutate a known field; unknown fragments keep bytes, order, identity.
	if err := m.Set(1, wiremsg.Value{Uint: 1 << 28}); err != nil {
		panic(err)
	}
	out, err := m.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nafter Set(1, 1<<28):  % x\n", out)
	fmt.Println("unknown fragments shifted but byte-identical: 38 01, 38 02, 4a 02 ff 00")

	// Schema upgrade: field 7 becomes known and typed; bytes do not change.
	up, err := wiremsg.Parse(v2, wire, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nupgraded to schema v2: field 7 values = %v\n", up.Get(7))
	round, _ := up.Marshal()
	fmt.Printf("re-marshal identical to input: %v\n", string(round) == string(wire))

	// Conflict: duplicate non-repeated field is rejected, never last-wins.
	bad := []byte{0x08, 0x01, 0x08, 0x02}
	if _, err := wiremsg.Parse(v1, bad, nil); err != nil {
		fmt.Printf("\nconflict rejected atomically: %v\n", err)
	}
}

// Command example demonstrates a full persistence round trip with the
// wirecodec package: compact binary encoding, zero-copy decoding, nested
// messages, packed repeated fields, and truncation safety.
package main

import (
	"encoding/hex"
	"fmt"
	"reflect"

	"wirecodec"
)

func main() {
	original := &wirecodec.Person{
		Name:    "Ada Lovelace",
		ID:      1843,
		Balance: -9876543210,
		Scores:  []int32{100, -3, 0, 42},
		Address: &wirecodec.Address{
			City: "Shanghai",
			Zip:  200000,
			Geo:  &wirecodec.Geo{Lat: 31.2304, Lng: 121.4737},
		},
		Tags:   []string{"engineer", "pioneer"},
		Avatar: []byte{0x89, 0x50, 0x4e, 0x47},
		Flags:  0xcafebabe,
		Ratio:  2.718281828459045,
	}

	// --- Persist: compact binary encoding ---
	data := original.Marshal()
	fmt.Printf("encoded %d bytes:\n%s\n\n", len(data), hex.Dump(data))

	// --- Restore: zero-copy decode ---
	var restored wirecodec.Person
	if err := restored.Unmarshal(data); err != nil {
		panic(err)
	}
	fmt.Printf("decoded: %+v\n", restored)
	fmt.Printf("  address: %+v geo=%+v\n", *restored.Address, *restored.Address.Geo)
	fmt.Printf("  round trip equal: %v\n\n", reflect.DeepEqual(original, &restored))

	// --- Forward compatibility: unknown fields are skipped ---
	w := wirecodec.NewWriter()
	w.Uint64Field(99, 12345) // field from a "future" schema
	w.StringField(100, "from the future")
	extended := append(append([]byte{}, data...), w.Bytes()...)
	var future wirecodec.Person
	if err := future.Unmarshal(extended); err != nil {
		panic(err)
	}
	fmt.Printf("forward compatible (unknown fields skipped): %v\n\n",
		reflect.DeepEqual(original, &future))

	// --- Safety: truncated input is reported, never panics ---
	truncated := data[:len(data)-3]
	var victim wirecodec.Person
	err := victim.Unmarshal(truncated)
	fmt.Printf("truncated stream (%d/%d bytes) -> error: %v\n",
		len(truncated), len(data), err)
}

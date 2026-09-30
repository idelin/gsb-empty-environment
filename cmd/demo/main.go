// Demo: an old (v1) program and a new (v2) program exchanging records.
package main

import (
	"encoding/hex"
	"fmt"

	"stablecodec/codec"
)

func personV1() *codec.Schema {
	s := codec.NewSchema("person", 1)
	s.AddField(codec.FieldDef{Number: 1, Name: "name", Type: codec.FBytes, Required: true})
	s.AddField(codec.FieldDef{Number: 2, Name: "id", Type: codec.FVarint, Required: true})
	return s
}

func personV2() *codec.Schema {
	s := codec.NewSchema("person", 2)
	s.AddField(codec.FieldDef{Number: 1, Name: "name", Type: codec.FBytes, Required: true})
	s.AddField(codec.FieldDef{Number: 2, Name: "id", Type: codec.FVarint, Required: true})
	s.ReserveDeleted(3, "fax removed in v2")                                 // deleted number: never reusable
	s.AddField(codec.FieldDef{Number: 4, Name: "email", Type: codec.FBytes}) // new optional
	s.AddField(codec.FieldDef{Number: 5, Name: "tags", Type: codec.FBytes, Repeated: true})
	return s
}

func main() {
	// 1. New program writes a record.
	m := personV2().NewMessage()
	m.SetString(1, "alice")
	m.SetUint64(2, 42)
	m.SetString(4, "alice@example.com")
	m.AppendString(5, "admin")
	m.AppendString(5, "owner")
	data, _ := m.Encode()
	fmt.Printf("v2 encoded %d bytes: %s\n\n", len(data), hex.EncodeToString(data))

	// 2. Old program reads it: unknown fields retained, version diagnosed.
	oldMsg, diag, err := personV1().Decode(data)
	fmt.Printf("v1 decode diagnostics:\n  %v\n\n", diag)
	name, _ := oldMsg.GetString(1)
	id, _ := oldMsg.GetUint64(2)
	fmt.Printf("v1 sees: name=%q id=%d (unknown fields kept: %d)\n\n", name, id, oldMsg.UnknownFieldCount())

	// 3. Old program re-saves; new program reads back: nothing lost.
	resaved, _ := oldMsg.Encode()
	back, _, _ := personV2().Decode(resaved)
	email, _ := back.GetString(4)
	fmt.Printf("after v1 transcode, v2 reads back: email=%q tags=%d\n\n", email, len(back.GetAll(5)))

	// 4. Schema safety: reusing the deleted number 3 is refused.
	if err := personV2().AddField(codec.FieldDef{Number: 3, Name: "nickname", Type: codec.FBytes}); err != nil {
		fmt.Printf("reuse of deleted #3 rejected: %v\n\n", err)
	}

	// 5. Missing required fields are reported by number.
	partial := personV1().NewMessage()
	partial.SetUint64(2, 7)
	bad, _ := partial.Encode()
	if _, _, err = personV1().Decode(bad); err != nil {
		fmt.Printf("missing required detected: %v\n", err)
	}
	_ = err
}

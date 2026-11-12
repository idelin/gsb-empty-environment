package hpack_test

import (
	"encoding/hex"
	"testing"

	"example.org/netsec/q0122/hpack"
)

// goldenBlocks were produced by the standard library's
// golang.org/x/net/http2/hpack.Encoder (Huffman enabled, dynamic table in
// use) via the helper in internal/gengolden. Regenerate after a Go toolchain
// upgrade if the encoding ever changes.
var goldenBlocks = []struct {
	hex    string
	fields []hpack.Field
}{
	{
		hex: "82878441882f91d35d055c87a77a8798f4485a980ae0",
		fields: []hpack.Field{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/"},
			{Name: ":authority", Value: "example.com"},
			{Name: "user-agent", Value: "golden/1.0"},
		},
	},
	{
		hex: "8387458b6075998ee163c1a498a947c05f8b1d75d0620d263d4c7441ea4086f2b12d424f4f0d68c3a96c6c6f2077c3b6726c64",
		fields: []hpack.Field{
			{Name: ":method", Value: "POST"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/api/v1/widgets"},
			{Name: ":authority", Value: "example.com"},
			{Name: "content-type", Value: "application/json"},
			{Name: "x-custom", Value: "héllo wörld"},
		},
	},
	{
		hex: "8287c0c2",
		fields: []hpack.Field{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/api/v1/widgets"},
			{Name: ":authority", Value: "example.com"},
		},
	},
}

func TestDecodeGoldenBlocks(t *testing.T) {
	dec := hpack.NewDecoder(4096, 4096)
	for i, gb := range goldenBlocks {
		block, err := hex.DecodeString(gb.hex)
		if err != nil {
			t.Fatalf("block %d: bad hex: %v", i, err)
		}
		got, err := dec.DecodeFull(block)
		if err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		if len(got) != len(gb.fields) {
			t.Fatalf("block %d: got %d fields, want %d", i, len(got), len(gb.fields))
		}
		for j, want := range gb.fields {
			if got[j] != want {
				t.Fatalf("block %d field %d: got %+v, want %+v", i, j, got[j], want)
			}
		}
	}
}

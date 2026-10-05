package batchack

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// FuzzDecodeCanonical checks the central invariant: any input Parse
// accepts must re-encode to itself byte for byte.
func FuzzDecodeCanonical(f *testing.F) {
	for _, seed := range [][]byte{
		{0, 0, 0, 0, 0},
		{0x04, 0, 0, 0, 0, 0x55},
		{0x0f, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 40, 0x01},
		{1<<3 | 0x04, 0, 0, 0, 0, 0, 0, 0, 3, 0x10},
		[]byte("\x0f0/00\x01000000@0000"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := Parse(data)
		if err != nil {
			return
		}
		if msg.Count() > 4096 {
			return // expansion is O(count); the canonicality checks are not
		}
		re, err := Encode(msg.Members())
		if err != nil {
			t.Fatalf("Parse(%x) ok but Encode of members failed: %v", data, err)
		}
		if !bytes.Equal(re, data) {
			t.Fatalf("Parse(%x) accepted non-canonical form, re-encodes to %x", data, re)
		}
	})
}

// FuzzEncodeRoundTrip interprets the input as little-endian uint32s and
// checks encode/parse round trips.
func FuzzEncodeRoundTrip(f *testing.F) {
	f.Add([]byte{1, 0, 0, 0, 2, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		var set []uint32
		for len(data) >= 4 && len(set) < 128 {
			set = append(set, binary.LittleEndian.Uint32(data))
			data = data[4:]
		}
		enc, err := Encode(set)
		if err != nil {
			return // unencodable span or too many ranges
		}
		msg, err := Parse(enc)
		if err != nil {
			t.Fatalf("Parse(Encode(%v)) = %v", set, err)
		}
		for _, n := range set {
			if !msg.Contains(n) {
				t.Fatalf("set %v: member %d missing from %x", set, n, enc)
			}
		}
		if got := msg.Count(); got > uint64(len(set)) {
			t.Fatalf("set %v: count %d > %d (enc %x)", set, got, len(set), enc)
		}
	})
}

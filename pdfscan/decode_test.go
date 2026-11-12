package pdfscan

import (
	"bytes"
	"testing"
)

func TestFlateRoundTripWithPredictor(t *testing.T) {
	// Two rows of 4 bytes using PNG Up (tag 2): each stored row is the
	// difference against the previous row.
	row0 := []byte{10, 20, 30, 40}
	row1 := []byte{11, 22, 33, 44}
	raw := bytes.NewBuffer(nil)
	raw.WriteByte(2) // Up predictor for row 0 (base is zero)
	raw.Write(row0)
	raw.WriteByte(2) // Up predictor for row 1
	raw.Write([]byte{row1[0] - row0[0], row1[1] - row0[1], row1[2] - row0[2], row1[3] - row0[3]})

	encoded := flateCompress(raw.Bytes())
	parms := &Dict{Map: map[string]*Value{
		"Predictor":        {Kind: KindInt, Int: 12},
		"Columns":          {Kind: KindInt, Int: 4},
		"Colors":           {Kind: KindInt, Int: 1},
		"BitsPerComponent": {Kind: KindInt, Int: 8},
	}}
	st := &Stream{
		Dict: &Dict{Map: map[string]*Value{
			"Filter":      {Kind: KindName, Str: []byte("FlateDecode")},
			"DecodeParms": {Kind: KindDict, Dict: parms},
		}},
		Raw: encoded,
	}
	got, err := decodeStream(st)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	want := append(append([]byte{}, row0...), row1...)
	if !bytes.Equal(got, want) {
		t.Fatalf("predictor output = %v, want %v", got, want)
	}
}

func TestUnsupportedFilterFailsClosed(t *testing.T) {
	st := &Stream{
		Dict: &Dict{Map: map[string]*Value{
			"Filter": {Kind: KindName, Str: []byte("ASCII85Decode")},
		}},
		Raw: []byte("ignored"),
	}
	if _, err := decodeStream(st); err == nil {
		t.Fatal("expected error for unsupported filter")
	}
}

func TestIdentityCryptFilterAllowed(t *testing.T) {
	st := &Stream{
		Dict: &Dict{Map: map[string]*Value{
			"Filter": {Kind: KindArray, Array: []*Value{
				{Kind: KindName, Str: []byte("Crypt")},
			}},
			"DecodeParms": {Kind: KindDict, Dict: &Dict{Map: map[string]*Value{
				"Name": {Kind: KindName, Str: []byte("Identity")},
			}}},
		}},
		Raw: []byte("plain"),
	}
	got, err := decodeStream(st)
	if err != nil {
		t.Fatalf("identity Crypt should be allowed: %v", err)
	}
	if string(got) != "plain" {
		t.Fatalf("got %q", got)
	}
}

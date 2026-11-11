package hpack

import (
	"reflect"
	"testing"
)

func TestRoundTripWithDynamicTable(t *testing.T) {
	enc := NewEncoder(4096)
	dec := NewDecoder(4096)

	req1 := []HeaderField{
		{":method", "GET"},
		{":path", "/tenants/a/items"},
		{"x-tenant", "alpha"},
	}
	block1 := enc.Encode(nil, req1)
	got1, err := dec.Decode(block1)
	if err != nil {
		t.Fatalf("decode req1: %v", err)
	}
	if !reflect.DeepEqual(got1, req1) {
		t.Fatalf("req1 mismatch: got %v want %v", got1, req1)
	}

	// Second identical request must compress to indexed representations.
	block2 := enc.Encode(nil, req1)
	if len(block2) >= len(block1) {
		t.Fatalf("second block not compressed: %d >= %d bytes", len(block2), len(block1))
	}
	got2, err := dec.Decode(block2)
	if err != nil {
		t.Fatalf("decode req2: %v", err)
	}
	if !reflect.DeepEqual(got2, req1) {
		t.Fatalf("req2 mismatch: got %v want %v", got2, req1)
	}
}

func TestTableSizeUpdateEmittedAfterReduction(t *testing.T) {
	enc := NewEncoder(4096)
	dec := NewDecoder(4096)

	enc.Encode(nil, []HeaderField{{"x-a", "1"}})
	enc.SetMaxDynamicTableSize(128)

	block := enc.Encode(nil, []HeaderField{{"x-b", "2"}})
	if block[0]&0xe0 != 0x20 {
		t.Fatalf("block does not start with a table size update: %#x", block[0])
	}
	got, err := dec.Decode(block)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, []HeaderField{{"x-b", "2"}}) {
		t.Fatalf("got %v", got)
	}
}

func TestClearTableEmitsZeroThenRestore(t *testing.T) {
	enc := NewEncoder(4096)
	dec := NewDecoder(4096)

	enc.Encode(nil, []HeaderField{{"x-tenant", "alpha"}})
	enc.ClearTable()
	block := enc.Encode(nil, []HeaderField{{"x-tenant", "beta"}})

	// First two representations must be table size updates 0 and 4096.
	n1, rest, err := readInt(block, 5)
	if err != nil || block[0]&0xe0 != 0x20 || n1 != 0 {
		t.Fatalf("first update = %d, %#x, err %v", n1, block[0], err)
	}
	n2, _, err := readInt(rest, 5)
	if err != nil || rest[0]&0xe0 != 0x20 || n2 != 4096 {
		t.Fatalf("second update = %d, %#x, err %v", n2, rest[0], err)
	}

	got, err := dec.Decode(block)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, []HeaderField{{"x-tenant", "beta"}}) {
		t.Fatalf("got %v", got)
	}
	if len(dec.dt.entries) != 1 {
		t.Fatalf("decoder table holds %d entries, want 1", len(dec.dt.entries))
	}
}

func TestDecoderRejectsOversizeTableUpdate(t *testing.T) {
	dec := NewDecoder(128)
	enc := NewEncoder(4096)
	block := enc.Encode(nil, []HeaderField{{"x-a", "1"}})
	// Encoder's first block has no update; craft one manually: update to 256.
	bad := appendInt(nil, 256, 5, 0x20)
	bad = append(bad, block...)
	if _, err := dec.Decode(bad); err == nil {
		t.Fatal("expected error for oversize table update")
	}
}

func TestDecoderRejectsHuffman(t *testing.T) {
	// Literal without indexing, new name, Huffman bit set on the name.
	block := []byte{0x00, 0x81, 0x00, 0x00}
	dec := NewDecoder(4096)
	if _, err := dec.Decode(block); err != ErrHuffmanUnsupported {
		t.Fatalf("err = %v, want ErrHuffmanUnsupported", err)
	}
}

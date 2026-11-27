package h2guard

import "testing"

func TestHPACKStaticIndexed(t *testing.T) {
	d := newHPACKDecoder(4096)
	// 0x82 = indexed index 2 -> :method GET
	fields, err := d.Decode([]byte{0x82})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != ":method" || fields[0].Value != "GET" {
		t.Fatalf("unexpected fields: %+v", fields)
	}
}

func TestHPACKDynamicTableRoundTrip(t *testing.T) {
	d := newHPACKDecoder(4096)
	// Literal with incremental indexing (6.2.1), inline name/value.
	block := []byte{0x40}
	block = append(block, encodeString("x-custom")...)
	block = append(block, encodeString("secret")...)
	if _, err := d.Decode(block); err != nil {
		t.Fatalf("indexing literal: %v", err)
	}
	if len(d.dynamic) != 1 {
		t.Fatalf("want 1 dynamic entry, got %d", len(d.dynamic))
	}
	// Static table has 61 entries, so dynamic index 1 is HPACK index 62.
	fields, err := d.Decode([]byte{0xbe}) // 0x80 | 62
	if err != nil {
		t.Fatalf("dynamic indexed decode: %v", err)
	}
	if fields[0].Name != "x-custom" || fields[0].Value != "secret" {
		t.Fatalf("dynamic entry mismatch: %+v", fields)
	}
}

func TestHPACKIntegerMultiByte(t *testing.T) {
	// Index 136 needs multi-byte integer encoding.
	// 136 - 127 = 9 -> bytes 0xff, 0x09
	d := newHPACKDecoder(4096)
	d.add("a", "b")
	d.add("c", "d")
	// Force enough dynamic entries so index 136 resolves, then decode it.
	for i := 0; i < 80; i++ {
		d.add("n", "v")
	}
	encoded := encodeInteger(0x80, 7, 136)
	if len(encoded) != 2 || encoded[0] != 0xff || encoded[1] != 0x09 {
		t.Fatalf("multi-byte integer encoding wrong: %v", encoded)
	}
	if _, err := d.Decode(encoded); err != nil {
		t.Fatalf("decode index 136: %v", err)
	}
}

func TestHPACKHuffmanRejected(t *testing.T) {
	d := newHPACKDecoder(4096)
	// H=1 string: 0x88 followed by 8 arbitrary "huffman" bytes.
	block := []byte{0x00, 0x88, 1, 2, 3, 4, 5, 6, 7, 8, 0x03, 'a', 'b', 'c'}
	_, err := d.Decode(block)
	ce, ok := AsConnError(err)
	if !ok || ce.Code != ErrCodeCompression {
		t.Fatalf("want COMPRESSION_ERROR for huffman string, got %v", err)
	}
}

func TestHPACKInvalidIndex(t *testing.T) {
	d := newHPACKDecoder(4096)
	if _, err := d.Decode([]byte{0xff, 0x7f}); err == nil {
		t.Fatal("out-of-range index must be rejected")
	}
}

func TestHPACKTableSizeUpdateEvicts(t *testing.T) {
	d := newHPACKDecoder(4096)
	d.add("x", "y")
	if len(d.dynamic) != 1 {
		t.Fatal("setup failed")
	}
	// Dynamic table size update (6.3) to zero: prefix 0x20, value 0.
	if _, err := d.Decode([]byte{0x20}); err != nil {
		t.Fatalf("size update: %v", err)
	}
	if len(d.dynamic) != 0 || d.size != 0 {
		t.Fatalf("table not evicted: %d entries size %d", len(d.dynamic), d.size)
	}
}

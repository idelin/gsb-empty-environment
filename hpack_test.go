package netsec

import (
	"errors"
	"testing"
)

func TestHPACKIndexedAndLiteral(t *testing.T) {
	var enc hpackEncoder
	enc.indexed(2) // :method: GET
	enc.indexed(6) // :scheme: http
	enc.indexed(4) // :path: /
	enc.literal(":authority", "example.com")

	d := newHPACKDecoder(4096, DefaultMaxHeaderListSize)
	fields, err := d.Decode(enc.buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []hpackHeaderField{
		{":method", "GET"},
		{":scheme", "http"},
		{":path", "/"},
		{":authority", "example.com"},
	}
	if len(fields) != len(want) {
		t.Fatalf("got %d fields, want %d", len(fields), len(want))
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field %d = %+v, want %+v", i, fields[i], want[i])
		}
	}
}

func TestHPACKDynamicTableRoundTrip(t *testing.T) {
	var enc hpackEncoder
	// Literal with incremental indexing: 0x40 prefix, name index 38 (host),
	// custom value.
	enc.appendInt(0, 6, 0x40)
	enc.appendString("custom-name", false)
	enc.appendString("custom-value", false)
	enc.appendInt(62, 7, 0x80) // first dynamic entry

	d := newHPACKDecoder(4096, DefaultMaxHeaderListSize)
	fields, err := d.Decode(enc.buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if fields[0] != fields[1] {
		t.Fatalf("dynamic reference mismatch: %+v vs %+v", fields[0], fields[1])
	}
	if fields[1].Name != "custom-name" || fields[1].Value != "custom-value" {
		t.Fatalf("bad dynamic entry: %+v", fields[1])
	}
}

func TestHPACKHuffmanRoundTrip(t *testing.T) {
	var enc hpackEncoder
	enc.literal(":authority", "www.example.com") // force plain path
	enc.appendInt(0, 4, 0)
	enc.appendString("x-huffman", true)
	enc.appendString("hello world", true)

	d := newHPACKDecoder(4096, DefaultMaxHeaderListSize)
	fields, err := d.Decode(enc.buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if fields[1].Name != "x-huffman" || fields[1].Value != "hello world" {
		t.Fatalf("huffman round trip failed: %+v", fields[1])
	}
}

func TestHPACKInvalidIndexRejected(t *testing.T) {
	var enc hpackEncoder
	enc.appendInt(1000, 7, 0x80)
	d := newHPACKDecoder(4096, DefaultMaxHeaderListSize)
	if _, err := d.Decode(enc.buf); !errors.Is(err, errHPACKIndex) {
		t.Fatalf("err = %v, want errHPACKIndex", err)
	}
}

func TestHPACKTableSizeUpdateEnforced(t *testing.T) {
	var enc hpackEncoder
	enc.tableSizeUpdate(8192) // exceeds the 4096 limit
	d := newHPACKDecoder(4096, DefaultMaxHeaderListSize)
	if _, err := d.Decode(enc.buf); !errors.Is(err, errHPACKTableSize) {
		t.Fatalf("err = %v, want errHPACKTableSize", err)
	}
}

func TestHPACKTruncatedIntegerRejected(t *testing.T) {
	// 0x7f starts a multi-byte integer that never terminates.
	d := newHPACKDecoder(4096, DefaultMaxHeaderListSize)
	if _, err := d.Decode([]byte{0x7f, 0x80, 0x80}); !errors.Is(err, errHPACKInteger) {
		t.Fatalf("err = %v, want errHPACKInteger", err)
	}
}

func TestHPACKBadHuffmanRejected(t *testing.T) {
	// A full 0xff byte is the 8-bit EOS symbol, which must never appear inside
	// an encoded string; three of them also leave an overlong padding suffix.
	d := newHPACKDecoder(4096, DefaultMaxHeaderListSize)
	// Literal-without-indexing, then a length-1 Huffman string whose single
	// byte can only be the 8-bit EOS symbol (illegal inside a string).
	if _, err := d.Decode([]byte{0x00, 0x81, 0xff}); !errors.Is(err, errHPACKHuffman) {
		t.Fatalf("err = %v, want errHPACKHuffman", err)
	}
}

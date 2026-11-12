package hpack

import "testing"

func TestSetMaxSizeAndDynamicTableSize(t *testing.T) {
	d := NewDecoder(128, 128)
	enc := newEncoder()
	enc.encodeFields(Field{Name: "name", Value: "value"})
	if _, err := d.DecodeFull(enc.bytes()); err != nil {
		t.Fatal(err)
	}
	if d.DynamicTableSize() == 0 {
		t.Fatal("expected occupied dynamic table")
	}
	d.SetMaxSize(0)
	if d.DynamicTableSize() != 0 || len(d.dyn) != 0 {
		t.Fatalf("SetMaxSize(0) did not evict: size=%d entries=%d", d.DynamicTableSize(), len(d.dyn))
	}

	// Clamped to allowedMax when SETTINGS asks for more.
	d2 := NewDecoder(10, 10)
	d2.SetMaxSize(9999)
	if d2.maxSize != 10 {
		t.Fatalf("maxSize = %d, want clamp to 10", d2.maxSize)
	}
}

func TestNewDecoderNormalizesInvertedLimits(t *testing.T) {
	d := NewDecoder(100, 5)
	if d.allowedMax < d.maxSize {
		t.Fatalf("allowedMax %d below maxSize %d", d.allowedMax, d.maxSize)
	}
}

func TestExportHuffmanEncodeRoundTrip(t *testing.T) {
	encoded := ExportHuffmanEncode(nil, []byte("exported"))
	decoded, err := huffmanDecode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "exported" {
		t.Fatalf("got %q", decoded)
	}
}

func TestHuffmanDecodeErrors(t *testing.T) {
	// EOS symbol itself (30 ones) must never appear in a legal string.
	eos := []byte{0xff, 0xff, 0xff, 0xff}
	if _, err := huffmanDecode(eos); err == nil {
		t.Fatal("EOS-containing data should be rejected")
	}
	// Truncated symbol with too many leftover bits.
	if _, err := huffmanDecode([]byte{0xfe}); err == nil {
		t.Fatal("truncated symbol should be rejected")
	}
}

func TestDecodeFullLiteralNameIndexOutOfRange(t *testing.T) {
	d := NewDecoder(4096, 4096)
	// Incremental-indexing literal referencing index 1000.
	block := appendVarint(nil, 1000, 6, 0x40)
	if _, err := d.DecodeFull(block); err == nil {
		t.Fatal("expected out-of-range name index error")
	}
}

func TestReadStringTruncation(t *testing.T) {
	d := NewDecoder(4096, 4096)
	// Plain string claiming 5 bytes but supplying 2.
	block := []byte{0x00, 0x05, 'a', 'b'}
	if _, err := d.DecodeFull(block); err == nil {
		t.Fatal("expected truncated string error")
	}
	// Never-indexed literal with an empty name and a Huffman value claiming
	// one byte that never arrives.
	block2 := []byte{0x10, 0x00, 0x81}
	if _, err := d.DecodeFull(block2); err == nil {
		t.Fatal("expected truncated Huffman string error")
	}
	// Length is satisfied but the Huffman content itself is invalid (EOS).
	block3 := []byte{0x10, 0x00, 0x84, 0xff, 0xff, 0xff, 0xff}
	if _, err := d.DecodeFull(block3); err == nil {
		t.Fatal("expected invalid Huffman content error")
	}
}

func TestReadVarintOverflow(t *testing.T) {
	d := NewDecoder(4096, 4096)
	// Indexed header with an ever-continuing varint.
	block := []byte{0xff}
	for i := 0; i < 12; i++ {
		block = append(block, 0x80)
	}
	if _, err := d.DecodeFull(block); err == nil {
		t.Fatal("expected varint overflow error")
	}
}

func TestFieldSize(t *testing.T) {
	f := Field{Name: "abc", Value: "de"}
	if got, want := f.Size(), uint32(3+2+32); got != want {
		t.Fatalf("Size = %d, want %d", got, want)
	}
}

func TestAtRejectsIndexZero(t *testing.T) {
	d := NewDecoder(4096, 4096)
	if _, ok := d.at(0); ok {
		t.Fatal("index 0 must never resolve")
	}
	if _, ok := d.at(99999); ok {
		t.Fatal("out-of-range index must not resolve")
	}
}

func TestAddEvictsUntilFitting(t *testing.T) {
	// A 100-byte table holds two ~40-byte entries but not a third, forcing
	// eviction of the oldest entry.
	d := NewDecoder(100, 100)
	enc := newEncoder()
	enc.encodeFields(
		Field{Name: "n1", Value: "v1"},
		Field{Name: "n2", Value: "v2"},
		Field{Name: "n3", Value: "v3"},
	)
	if _, err := d.DecodeFull(enc.bytes()); err != nil {
		t.Fatal(err)
	}
	if d.DynamicTableSize() > d.maxSize {
		t.Fatalf("table size %d exceeds max %d", d.DynamicTableSize(), d.maxSize)
	}
	// The newest entry must still resolve at dynamic index 62.
	f, ok := d.at(62)
	if !ok || f.Name != "n3" {
		t.Fatalf("newest entry wrong: %+v ok=%v", f, ok)
	}
}

func TestHuffmanInvalidNodeAndBadPadding(t *testing.T) {
	// 0xff starts the 30-bit all-ones EOS code; one byte cannot complete it,
	// and its padding check fails because EOS needs 30 bits.
	if _, err := huffmanDecode([]byte{0xff}); err == nil {
		t.Fatal("incomplete EOS should be rejected")
	}
	// All-zero trailing padding violates the EOS-prefix requirement.
	enc := newEncoder()
	enc.huffman = true
	enc.appendString("a")
	raw := enc.bytes()
	raw[len(raw)-1] &^= 0x01 // clear the lowest padding bit to zero
	if _, err := huffmanDecode(raw[1:]); err == nil {
		t.Fatal("non-one padding bits should be rejected")
	}
}

func TestHuffmanDecodeNilAndEmpty(t *testing.T) {
	out, err := huffmanDecode(nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("nil input: out=%v err=%v", out, err)
	}
}

func TestSizeUpdateTruncatedVarint(t *testing.T) {
	d := NewDecoder(4096, 4096)
	// 0x3f starts a size update with all prefix bits set, 0x80 continues but
	// never terminates.
	if _, err := d.DecodeFull([]byte{0x3f, 0x80}); err == nil {
		t.Fatal("expected truncated size-update varint error")
	}
}

func TestOversizedEntryEmptiesTable(t *testing.T) {
	d := NewDecoder(36, 36) // smaller than any name+value+32 entry used below
	enc := newEncoder()
	enc.encodeFields(Field{Name: "name", Value: "value"})
	if _, err := d.DecodeFull(enc.bytes()); err != nil {
		t.Fatal(err)
	}
	if d.DynamicTableSize() != 0 || len(d.dyn) != 0 {
		t.Fatalf("entry exceeding max size must empty the table: %+v", d.dyn)
	}
}

func TestHuffmanDecodeInvalidNode(t *testing.T) {
	// Code 0x00 is an 8-bit prefix that no symbol occupies, so the trie
	// lookup lands on a nil child immediately.
	if _, err := huffmanDecode([]byte{0x00}); err == nil {
		t.Fatal("expected invalid Huffman node error")
	}
}

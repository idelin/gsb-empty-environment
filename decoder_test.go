package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeIndexedStaticHeader(t *testing.T) {
	fields, err := Decode([]byte{0x82}, 4096)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != ":method" || fields[0].Value != "GET" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
}

func TestDynamicTableSizeUpdateAfterFirstHeaderFails(t *testing.T) {
	block := append([]byte{0x82}, encodeInt(5, 0, 0x20)...)
	_, err := Decode(block, 4096)
	if !errors.Is(err, ErrSizeUpdateNotAtStart) {
		t.Fatalf("got error %v, want ErrSizeUpdateNotAtStart", err)
	}
}

func TestDynamicTableSizeUpdateAfterLiteralHeaderFails(t *testing.T) {
	block := append(literalWithoutIndexing("x-test", "one"), encodeInt(5, 128, 0x20)...)
	_, err := Decode(block, 4096)
	if !errors.Is(err, ErrSizeUpdateNotAtStart) {
		t.Fatalf("got error %v, want ErrSizeUpdateNotAtStart", err)
	}
}

func TestDynamicTableSizeUpdateAboveSettingsLimitFails(t *testing.T) {
	block := encodeInt(5, 4097, 0x20)
	_, err := Decode(block, 4096)
	if !errors.Is(err, ErrSizeUpdateTooLarge) {
		t.Fatalf("got error %v, want ErrSizeUpdateTooLarge", err)
	}
}

func TestDynamicTableSizeUpdateAtSettingsLimitSucceeds(t *testing.T) {
	block := append(encodeInt(5, 4096, 0x20), 0x82)
	fields, err := Decode(block, 4096)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != ":method" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
}

func TestConsecutiveSizeUpdatesAtFrontSucceed(t *testing.T) {
	block := append(encodeInt(5, 0, 0x20), encodeInt(5, 1024, 0x20)...)
	block = append(block, literalWithoutIndexing("x-after-updates", "ok")...)

	fields, err := Decode(block, 4096)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != "x-after-updates" || fields[0].Value != "ok" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
}

func TestEncodedUpdateMustRespectChangedSettingsLimit(t *testing.T) {
	d := NewDecoder(4096)
	if _, err := d.Decode(literalWithIncrementalIndexing("x", "y")); err != nil {
		t.Fatalf("seed Decode: %v", err)
	}

	d.SetAllowedMaxDynamicTableSize(16)
	if d.DynamicTableLen() != 0 {
		t.Fatalf("lowering SETTINGS limit should evict entries, got %d", d.DynamicTableLen())
	}
	_, err := d.Decode(encodeInt(5, 17, 0x20))
	if !errors.Is(err, ErrSizeUpdateTooLarge) {
		t.Fatalf("got error %v, want ErrSizeUpdateTooLarge", err)
	}
}

func TestShrinkAtFrontThenLiteralStillDecodes(t *testing.T) {
	d := NewDecoder(4096)

	fields, err := d.Decode(literalWithIncrementalIndexing("x-old", "old-value"))
	if err != nil {
		t.Fatalf("seed Decode: %v", err)
	}
	if len(fields) != 1 || d.DynamicTableLen() != 1 {
		t.Fatalf("seed fields=%#v dynamicLen=%d", fields, d.DynamicTableLen())
	}

	block := append(encodeInt(5, 0, 0x20), literalWithoutIndexing("x-new", "new-value")...)
	fields, err = d.Decode(block)
	if err != nil {
		t.Fatalf("shrink Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != "x-new" || fields[0].Value != "new-value" {
		t.Fatalf("unexpected fields after shrink: %#v", fields)
	}
	if d.DynamicTableLen() != 0 {
		t.Fatalf("dynamic table should be empty after shrinking to zero, got %d entries", d.DynamicTableLen())
	}

	_, err = d.Decode([]byte{0xbe}) // dynamic index 62
	if !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("evicted index error = %v, want ErrIndexOutOfRange", err)
	}
}

func TestDynamicTableEvictsOldestEntry(t *testing.T) {
	d := NewDecoder(64)
	if _, err := d.Decode(append(
		literalWithIncrementalIndexing("x-a", "1234567890"),
		literalWithIncrementalIndexing("x-b", "1234567890")...,
	)); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if d.DynamicTableLen() != 1 {
		t.Fatalf("dynamic table length = %d, want 1 after eviction", d.DynamicTableLen())
	}

	fields, err := d.Decode([]byte{0xbe})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != "x-b" {
		t.Fatalf("oldest entry was not evicted: %#v", fields)
	}
}

func TestIndexOutOfRangeFails(t *testing.T) {
	for _, block := range [][]byte{
		{0x80},       // index 0
		{0xbe},       // first dynamic index with an empty dynamic table
		{0xff, 0x00}, // index 127
	} {
		_, err := Decode(block, 4096)
		if !errors.Is(err, ErrIndexOutOfRange) {
			t.Fatalf("block %x: got error %v, want ErrIndexOutOfRange", block, err)
		}
	}
}

func TestNeverIndexedLiteralIsMarkedSensitive(t *testing.T) {
	block := []byte{0x10}
	block = append(block, encodeString("authorization")...)
	block = append(block, encodeString("secret")...)

	fields, err := Decode(block, 4096)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != "authorization" || !fields[0].Sensitive {
		t.Fatalf("unexpected field: %#v", fields)
	}
}

func TestDynamicIndexUsesNewestEntryFirst(t *testing.T) {
	d := NewDecoder(4096)
	_, err := d.Decode(append(
		literalWithIncrementalIndexing("x-index", "first"),
		literalWithIncrementalIndexing("x-index", "second")...,
	))
	if err != nil {
		t.Fatalf("seed Decode: %v", err)
	}

	fields, err := d.Decode([]byte{0xbe, 0xbf}) // indices 62 and 63
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 2 ||
		fields[0].Name != "x-index" || fields[0].Value != "second" ||
		fields[1].Name != "x-index" || fields[1].Value != "first" {
		t.Fatalf("unexpected dynamic index order: %#v", fields)
	}
}

func TestLiteralCanReferenceDynamicName(t *testing.T) {
	d := NewDecoder(4096)
	if _, err := d.Decode(literalWithIncrementalIndexing("x-dynamic-name", "first")); err != nil {
		t.Fatalf("seed Decode: %v", err)
	}

	block := encodeInt(4, 62, 0x00)
	block = append(block, encodeString("second")...)
	fields, err := d.Decode(block)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != "x-dynamic-name" || fields[0].Value != "second" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
}

func TestOversizedEntryClearsDynamicTable(t *testing.T) {
	d := NewDecoder(64)
	if _, err := d.Decode(literalWithIncrementalIndexing("x", "y")); err != nil {
		t.Fatalf("seed Decode: %v", err)
	}
	if d.DynamicTableLen() != 1 {
		t.Fatalf("dynamic table length = %d, want 1", d.DynamicTableLen())
	}

	fields, err := d.Decode(literalWithIncrementalIndexing("x-large", strings.Repeat("v", 100)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != "x-large" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
	if d.DynamicTableLen() != 0 {
		t.Fatalf("oversized entry should empty the table, got %d entries", d.DynamicTableLen())
	}
}

func TestDecodeHuffmanString(t *testing.T) {
	// RFC 7541, Appendix C.4.3: :authority: www.example.com, with the
	// value encoded using the HPACK Huffman table.
	block := []byte{
		0x41, 0x8c, 0xf1, 0xe3, 0xc2, 0xe5, 0xf2, 0x3a,
		0x6b, 0xa0, 0xab, 0x90, 0xf4, 0xff,
	}
	fields, err := Decode(block, 4096)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != ":authority" || fields[0].Value != "www.example.com" {
		t.Fatalf("unexpected Huffman field: %#v", fields)
	}
}

func TestTruncatedBlockFails(t *testing.T) {
	for _, block := range [][]byte{
		{0x40},
		{0x40, 0x05, 'x'},
		{0x3f},
	} {
		_, err := Decode(block, 4096)
		if err == nil {
			t.Fatalf("block %x decoded without error", block)
		}
	}
}

func TestIntegerOverflowFails(t *testing.T) {
	block := []byte{0xff}
	for i := 0; i < 10; i++ {
		block = append(block, 0xff)
	}
	block = append(block, 0x7f)
	_, err := Decode(block, 4096)
	if !errors.Is(err, ErrIntegerOverflow) {
		t.Fatalf("got error %v, want ErrIntegerOverflow", err)
	}
}

func TestInvalidHuffmanPaddingFails(t *testing.T) {
	// Huffman length 1, followed by eight trailing one bits. HPACK padding
	// may contain at most seven bits.
	_, err := Decode([]byte{0x40, 0x01, 'x', 0x81, 0xff}, 4096)
	if !errors.Is(err, ErrInvalidHuffman) {
		t.Fatalf("got error %v, want ErrInvalidHuffman", err)
	}
}

func encodeInt(prefix uint8, value uint64, first byte) []byte {
	max := uint64(1)<<prefix - 1
	if value < max {
		return []byte{first | byte(value)}
	}

	out := []byte{first | byte(max)}
	value -= max
	for value >= 128 {
		out = append(out, byte(value%128+128))
		value /= 128
	}
	return append(out, byte(value))
}

func encodeString(s string) []byte {
	return append(encodeInt(7, uint64(len(s)), 0), s...)
}

func literalWithoutIndexing(name, value string) []byte {
	out := []byte{0x00}
	out = append(out, encodeString(name)...)
	return append(out, encodeString(value)...)
}

func literalWithIncrementalIndexing(name, value string) []byte {
	out := []byte{0x40}
	out = append(out, encodeString(name)...)
	return append(out, encodeString(value)...)
}

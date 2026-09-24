package llex

import (
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// rawTable builds a table header byte string without any validation, so
// tests can forge invalid tables.
func rawTable(L int, ids []string, lengths []int) []byte {
	out := []byte(tableMagic)
	out = append(out, wireVersion, byte(L))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ids)))
	for i, id := range ids {
		out = binary.LittleEndian.AppendUint16(out, uint16(len(id)))
		out = append(out, id...)
		out = append(out, byte(lengths[i]))
	}
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	return out
}

func rawBlock(L int, ids []string, lengths []int, dataBits uint32, data []byte) []byte {
	out := rawTable(L, ids, lengths)
	out = binary.LittleEndian.AppendUint32(out, dataBits)
	return append(out, data...)
}

func TestTableRoundTrip(t *testing.T) {
	tab, err := SolveLengths(makeSymbols([]uint64{5, 3, 0, 9, 1}), 6)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalTable(tab)
	if err != nil {
		t.Fatal(err)
	}
	code, n, err := ParseTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(raw) {
		t.Fatalf("consumed %d of %d bytes", n, len(raw))
	}
	for i := range tab.IDs {
		if code.IDs[i] != tab.IDs[i] || code.Lengths[i] != tab.Lengths[i] {
			t.Fatalf("round trip mismatch at %d", i)
		}
	}
}

func TestRejectOversubscribedTable(t *testing.T) {
	// Three length-1 codewords: Kraft sum 3/2 > 1.
	raw := rawTable(1, []string{"a", "b", "c"}, []int{1, 1, 1})
	if _, _, err := ParseTable(raw); err == nil || !strings.Contains(err.Error(), "oversubscribed") {
		t.Fatalf("oversubscribed table must be rejected, got %v", err)
	}
}

func TestRejectBadChecksum(t *testing.T) {
	raw := rawTable(2, []string{"a", "b"}, []int{1, 1})
	raw[12] ^= 0xff // corrupt inside the checksummed region
	if _, _, err := ParseTable(raw); err == nil {
		t.Fatal("corrupted table must be rejected")
	}
}

func TestRejectLengthOutOfRange(t *testing.T) {
	if _, _, err := ParseTable(rawTable(3, []string{"a"}, []int{0})); err == nil {
		t.Fatal("length 0 must be rejected")
	}
	if _, _, err := ParseTable(rawTable(3, []string{"a"}, []int{4})); err == nil {
		t.Fatal("length > L must be rejected")
	}
}

func TestRejectDuplicateAndUnsortedIDs(t *testing.T) {
	if _, _, err := ParseTable(rawTable(2, []string{"a", "a"}, []int{1, 1})); err == nil {
		t.Fatal("duplicate IDs must be rejected")
	}
	if _, _, err := ParseTable(rawTable(2, []string{"b", "a"}, []int{1, 1})); err == nil {
		t.Fatal("unsorted IDs must be rejected")
	}
}

func TestRejectDeclaredCountOverflow(t *testing.T) {
	// A huge declared symbol count must not trigger a huge allocation.
	raw := []byte(tableMagic)
	raw = append(raw, wireVersion, 4)
	raw = binary.LittleEndian.AppendUint32(raw, 0xffffffff)
	raw = binary.LittleEndian.AppendUint32(raw, crc32.ChecksumIEEE(raw))
	if _, _, err := ParseTable(raw); err == nil {
		t.Fatal("impossible symbol count must be rejected")
	}
}

func TestRejectNonCanonicalPadding(t *testing.T) {
	// One data bit (0) followed by non-zero padding bits.
	blk := rawBlock(1, []string{"a", "b"}, []int{1, 1}, 1, []byte{0b00000001})
	if _, _, err := DecodeBlockData(blk, -1); err == nil || !strings.Contains(err.Error(), "padding") {
		t.Fatalf("non-zero padding must be rejected, got %v", err)
	}
}

func TestRejectIllegalCodeword(t *testing.T) {
	// Incomplete table: a=00, b=01. The bit pattern 10 is a hole and must
	// be rejected, not completed into an arbitrary symbol.
	blk := rawBlock(2, []string{"a", "b"}, []int{2, 2}, 3, []byte{0b10100000})
	if _, _, err := DecodeBlockData(blk, -1); err == nil || !strings.Contains(err.Error(), "illegal codeword") {
		t.Fatalf("undefined codeword must be rejected, got %v", err)
	}
}

func TestIncompleteTableDecodesDefinedOnly(t *testing.T) {
	// a=00, b=01; stream "0001" decodes to a,b.
	blk := rawBlock(2, []string{"a", "b"}, []int{2, 2}, 4, []byte{0b00010000})
	syms, _, err := DecodeBlockData(blk, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 2 || syms[0] != "a" || syms[1] != "b" {
		t.Fatalf("got %v, want [a b]", syms)
	}
}

func TestRejectTruncatedCodeword(t *testing.T) {
	// Single bit 0: not a complete codeword for a length-2 table.
	blk := rawBlock(2, []string{"a", "b"}, []int{2, 2}, 1, []byte{0b00000000})
	if _, _, err := DecodeBlockData(blk, -1); err == nil || !strings.Contains(err.Error(), "mid-codeword") {
		t.Fatalf("truncated codeword must be rejected, got %v", err)
	}
}

func TestOutputLimitEnforced(t *testing.T) {
	blk := rawBlock(1, []string{"a", "b"}, []int{1, 1}, 4, []byte{0b01010000})
	if _, _, err := DecodeBlockData(blk, 2); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("output limit must be enforced, got %v", err)
	}
	syms, _, err := DecodeBlockData(blk, 4)
	if err != nil || len(syms) != 4 {
		t.Fatalf("got %v, %v", syms, err)
	}
}

func TestBlockRoundTrip(t *testing.T) {
	syms := []Symbol{{ID: "alpha", Freq: 10}, {ID: "beta", Freq: 4}, {ID: "gamma", Freq: 1}}
	tab, err := SolveLengths(syms, 5)
	if err != nil {
		t.Fatal(err)
	}
	tokens := []uint32{0, 1, 0, 2, 0, 1, 0, 0}
	blk, err := MarshalBlock(tab, tokens)
	if err != nil {
		t.Fatal(err)
	}
	out, consumed, err := DecodeBlockData(blk, -1)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(blk) {
		t.Fatalf("consumed %d of %d", consumed, len(blk))
	}
	if len(out) != len(tokens) {
		t.Fatalf("got %d symbols, want %d", len(out), len(tokens))
	}
	for i, tok := range tokens {
		if out[i] != tab.IDs[tok] {
			t.Fatalf("position %d: got %q want %q", i, out[i], tab.IDs[tok])
		}
	}
}

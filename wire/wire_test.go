package wire

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

func expectError(t *testing.T, b []byte, lim Limits, schema *Schema,
	wantOp string, wantOff int64, wantPath []uint32) *Error {
	t.Helper()
	_, err := drain(NewParser(lim, schema), b, 0)
	if err == nil {
		t.Fatalf("input %x: expected %s error, got nil", b, wantOp)
	}
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("input %x: error %v is not a *Error", b, err)
	}
	if pe.Op != wantOp {
		t.Fatalf("input %x: op %q, want %q (err %v)", b, pe.Op, wantOp, pe)
	}
	if pe.Offset != wantOff {
		t.Fatalf("input %x: offset %d, want %d (err %v)", b, pe.Offset, wantOff, pe)
	}
	if !reflect.DeepEqual(pe.Path, wantPath) {
		t.Fatalf("input %x: path %v, want %v (err %v)", b, pe.Path, wantPath, pe)
	}
	return pe
}

func TestVarintOverflow(t *testing.T) {
	// 11-byte varint: 10th byte still has the continuation bit.
	expectError(t, unhex(t, "08 ffffffffffffffffffff 01"), Limits{}, nil, "varint", 1, nil)
	// 10-byte varint whose 10th byte exceeds 1.
	expectError(t, unhex(t, "08 ffffffffffffffffff02"), Limits{}, nil, "varint", 1, nil)
	// Overflow inside a nested message: offset is absolute, path is set.
	schema := &Schema{Nested: map[uint32]*Schema{4: nil}}
	pe := expectError(t, unhex(t, "22 0b 08 ffffffffffffffffff02"), Limits{}, schema,
		"varint", 3, []uint32{4})
	if !strings.Contains(pe.Error(), "field path 4") {
		t.Fatalf("error text missing field path: %v", pe)
	}
}

func TestInvalidFieldNumber(t *testing.T) {
	// Field number 0.
	expectError(t, unhex(t, "00 01"), Limits{}, nil, "key", 0, nil)
	// Field number 2^29 (one past the maximum).
	expectError(t, unhex(t, "80808080 10 01"), Limits{}, nil, "key", 0, nil)
}

func TestUnsupportedWireTypes(t *testing.T) {
	for _, key := range []string{"0b", "0c", "0e", "0f"} { // types 3, 4, 6, 7
		expectError(t, unhex(t, key+" 00"), Limits{}, nil, "key", 0, nil)
	}
}

func TestPackedFixedWidthNotDivisible(t *testing.T) {
	schema32 := &Schema{Packed: map[uint32]int{5: TypeFixed32}}
	expectError(t, unhex(t, "2a 05 0102030405"), Limits{}, schema32,
		"packed", 2, []uint32{5})
	schema64 := &Schema{Packed: map[uint32]int{5: TypeFixed64}}
	expectError(t, unhex(t, "2a 07 01020304050607"), Limits{}, schema64,
		"packed", 2, []uint32{5})
	// Truncated varint inside a packed varint array.
	schemaV := &Schema{Packed: map[uint32]int{5: TypeVarint}}
	expectError(t, unhex(t, "2a 02 0180"), Limits{}, schemaV,
		"packed", 3, []uint32{5})
}

func TestNestedErrorOffsetAndPath(t *testing.T) {
	schema := &Schema{Nested: map[uint32]*Schema{4: nil}}
	// Nested payload starts at offset 2; key 0x0b is wire type 3.
	expectError(t, unhex(t, "22 02 0b00"), Limits{}, schema, "key", 2, []uint32{4})
	// Two levels deep: path accumulates.
	deep := &Schema{Nested: map[uint32]*Schema{4: schema}}
	expectError(t, unhex(t, "22 04 2202 0b00"), Limits{}, deep, "key", 4, []uint32{4, 4})
}

func TestLimitTotalBytes(t *testing.T) {
	b := unhex(t, fxRepeated)
	lim := Limits{MaxTotalBytes: int64(len(b) - 1)}
	expectError(t, b, lim, nil, "limit", 0, nil)
	// Exactly at the limit is fine.
	mustParse(t, b, Limits{MaxTotalBytes: int64(len(b))}, nil)
}

func TestLimitFieldLength(t *testing.T) {
	b := unhex(t, "12 03 aabbcc")
	// The length varint sits at offset 1; the check fires before allocation.
	expectError(t, b, Limits{MaxFieldLen: 2}, nil, "limit", 1, nil)
	mustParse(t, b, Limits{MaxFieldLen: 3}, nil)
}

func TestLimitFieldCount(t *testing.T) {
	b := unhex(t, "08 01 08 02")
	expectError(t, b, Limits{MaxFields: 1}, nil, "limit", 2, nil)
	mustParse(t, b, Limits{MaxFields: 2}, nil)
	// The limit applies per nesting level, not globally.
	schema := &Schema{Nested: map[uint32]*Schema{4: nil}}
	mustParse(t, unhex(t, "22 03 089601 0801"), Limits{MaxFields: 2}, schema)
}

func TestLimitDepth(t *testing.T) {
	schema := &Schema{}
	schema.Nested = map[uint32]*Schema{4: schema} // field 4 nests its own schema
	b := unhex(t, "22 02 2200")                   // root -> f4 -> f4(empty)
	expectError(t, b, Limits{MaxDepth: 2}, schema, "limit", 4, []uint32{4, 4})
	mustParse(t, b, Limits{MaxDepth: 3}, schema)
	mustParse(t, b, Limits{}, schema) // 0 = unlimited
}

func TestRoundTripPreservesUnknownFields(t *testing.T) {
	// No schema at all: every field is unknown and must survive a
	// decode/re-encode cycle byte-for-byte, in order.
	b := unhex(t, fxComposite)
	m := mustParse(t, b, Limits{}, nil)
	if got := m.Marshal(); !bytes.Equal(got, b) {
		t.Fatalf("round trip mismatch:\n got %x\nwant %x", got, b)
	}
	// Unknown fields inside a schema-declared nested message are kept too.
	m2 := mustParse(t, b, Limits{}, fxSchema)
	if got := m2.Marshal(); !bytes.Equal(got, b) {
		t.Fatalf("round trip with schema mismatch:\n got %x\nwant %x", got, b)
	}
}

func TestEmptyInput(t *testing.T) {
	m := mustParse(t, nil, Limits{}, nil)
	if len(m.Fields) != 0 {
		t.Fatalf("got %d fields, want 0", len(m.Fields))
	}
	if len(m.Marshal()) != 0 {
		t.Fatal("empty message should re-encode to empty")
	}
}

func TestNeedMoreAndResume(t *testing.T) {
	b := unhex(t, fxRepeated)
	p := NewParser(Limits{}, nil)
	if _, err := p.Next(); err != ErrNeedMore {
		t.Fatalf("Next on empty parser: %v", err)
	}
	if err := p.Feed(b[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Next(); err != ErrNeedMore {
		t.Fatalf("Next with partial field: %v", err)
	}
	if err := p.Feed(b[1:2]); err != nil {
		t.Fatal(err)
	}
	f, err := p.Next()
	if err != nil {
		t.Fatalf("Next after completing field: %v", err)
	}
	if f.Number != 1 || f.Value != 1 {
		t.Fatalf("got field %d value %d", f.Number, f.Value)
	}
	if p.Offset() != 2 {
		t.Fatalf("offset %d, want 2", p.Offset())
	}
}

func TestFeedAfterFinish(t *testing.T) {
	p := NewParser(Limits{}, nil)
	if err := p.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := p.Feed([]byte{0x08}); err == nil {
		t.Fatal("Feed after Finish should fail")
	}
	if _, err := p.Next(); err != io.EOF {
		t.Fatalf("Next after Finish on empty input: %v", err)
	}
}

func TestStatsRecorded(t *testing.T) {
	b := unhex(t, fxComposite)
	p := NewParser(Limits{}, fxSchema)
	if _, err := drain(p, b, 5); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	if st.BytesFed != int64(len(b)) {
		t.Errorf("BytesFed %d, want %d", st.BytesFed, len(b))
	}
	if st.FieldsParsed != 8 {
		t.Errorf("FieldsParsed %d, want 8", st.FieldsParsed)
	}
	if st.MessagesParsed != 1 {
		t.Errorf("MessagesParsed %d, want 1", st.MessagesParsed)
	}
	if st.PeakBuffered <= 0 || st.PeakBuffered > len(b) {
		t.Errorf("PeakBuffered %d out of range", st.PeakBuffered)
	}
	if st.BytesAllocated <= 0 {
		t.Errorf("BytesAllocated %d, want > 0", st.BytesAllocated)
	}
}

func TestDump(t *testing.T) {
	m := mustParse(t, unhex(t, fxComposite), Limits{}, fxSchema)
	var sb strings.Builder
	if err := m.WriteDump(&sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		"1: varint 300",
		`2: bytes len=2 6869`,
		"4: message {",
		"  1: varint 150",
		"5: packed fixed32 [1 2]",
		"3: fixed32 305419896",
		"6: fixed64 1234605616436508552",
		"7: packed varint [1 300]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q:\n%s", want, out)
		}
	}
}

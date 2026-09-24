package wiremsg

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"
)

// --- independent byte-level reference encoder (test-only) ---

func refVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func refTag(num uint32, wt int) []byte { return refVarint(uint64(num)<<3 | uint64(wt)) }

func refVarintField(num uint32, v uint64) []byte {
	return append(refTag(num, WireVarint), refVarint(v)...)
}

func refFixed32Field(num uint32, v uint32) []byte {
	out := refTag(num, WireFixed32)
	return append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func refBytesField(num uint32, payload []byte) []byte {
	out := refTag(num, WireBytes)
	out = append(out, refVarint(uint64(len(payload)))...)
	return append(out, payload...)
}

func concat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

var schemaV1 = &Schema{Fields: map[uint32]FieldSchema{
	1: {Kind: KindVarint},
	2: {Kind: KindString},
	4: {Kind: KindMessage, Nested: &Schema{Fields: map[uint32]FieldSchema{
		1: {Kind: KindFixed32},
	}}},
	5: {Kind: KindVarint, Repeated: true},
}}

// schemaV2 upgrades field 3 (previously unknown) to a known varint.
var schemaV2 = &Schema{Fields: map[uint32]FieldSchema{
	1: {Kind: KindVarint},
	2: {Kind: KindString},
	3: {Kind: KindVarint},
	4: {Kind: KindMessage, Nested: &Schema{Fields: map[uint32]FieldSchema{
		1: {Kind: KindFixed32},
	}}},
	5: {Kind: KindVarint, Repeated: true},
}}

func TestDecodeKnownAndUnknownZeroCopy(t *testing.T) {
	input := concat(
		refVarintField(1, 150),
		refBytesField(7, []byte{0xAA, 0xBB}), // unknown, kept raw
		refBytesField(2, []byte("hi")),
	)
	m, u, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if u.Fields != 3 || u.Depth != 1 {
		t.Fatalf("usage = %+v", u)
	}
	segs := m.Segments()
	if len(segs) != 3 {
		t.Fatalf("segments = %d", len(segs))
	}
	if v, _ := m.Get(1); v.Kind != KindVarint || v.U64 != 150 {
		t.Fatalf("field 1 = %+v", v)
	}
	if v, _ := m.Get(2); v.Kind != KindString || v.Str != "hi" {
		t.Fatalf("field 2 = %+v", v)
	}
	raw := segs[1].Raw
	if !bytes.Equal(raw, refBytesField(7, []byte{0xAA, 0xBB})) {
		t.Fatalf("raw = %x", raw)
	}
	// Raw must be a shared subslice of the input (zero-copy).
	if unsafe.SliceData(raw) != unsafe.SliceData(input[len(refVarintField(1, 150)):]) {
		t.Fatal("unknown raw segment does not share the input buffer")
	}
	// Round-trip is byte-identical.
	if got := m.Encode(); !bytes.Equal(got, input) {
		t.Fatalf("round-trip = %x, want %x", got, input)
	}
}

func TestUnknownRepeatedNotOverwritten(t *testing.T) {
	input := concat(
		refVarintField(9, 1),
		refVarintField(1, 42),
		refVarintField(9, 2),
		refVarintField(9, 3),
	)
	m, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	var raws [][]byte
	for _, s := range m.Segments() {
		if s.Num == 9 {
			raws = append(raws, s.Raw)
		}
	}
	if len(raws) != 3 {
		t.Fatalf("unknown field 9 occurrences = %d, want 3 (map would lose data)", len(raws))
	}
	for i, want := range [][]byte{refVarintField(9, 1), refVarintField(9, 2), refVarintField(9, 3)} {
		if !bytes.Equal(raws[i], want) {
			t.Fatalf("occurrence %d = %x, want %x (relative order lost)", i, raws[i], want)
		}
	}
	if got := m.Encode(); !bytes.Equal(got, input) {
		t.Fatalf("round-trip = %x, want %x", got, input)
	}
}

func TestNestedUnknownAndKnown(t *testing.T) {
	inner := concat(
		refFixed32Field(1, 0xDEADBEEF),       // known nested
		refBytesField(8, []byte{1, 2, 3, 4}), // unknown nested, kept raw
	)
	input := refBytesField(4, inner)
	m, u, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if u.Depth != 2 {
		t.Fatalf("depth = %d, want 2", u.Depth)
	}
	v, ok := m.Get(4)
	if !ok || v.Kind != KindMessage {
		t.Fatalf("field 4 = %+v", v)
	}
	sub := v.Msg.Segments()
	if len(sub) != 2 || !sub[0].Known || sub[0].Value.U32 != 0xDEADBEEF {
		t.Fatalf("nested = %+v", sub)
	}
	if sub[1].Known || !bytes.Equal(sub[1].Raw, refBytesField(8, []byte{1, 2, 3, 4})) {
		t.Fatalf("nested unknown = %+v", sub[1])
	}
	if got := m.Encode(); !bytes.Equal(got, input) {
		t.Fatalf("round-trip = %x, want %x", got, input)
	}
}

func TestModifyKnownPreservesOthersByteForByte(t *testing.T) {
	unknownA := refBytesField(6, []byte("keep-me"))
	unknownB := refVarintField(9, 7)
	input := concat(unknownA, refVarintField(1, 1), unknownB, refBytesField(2, []byte("x")))
	m, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	// Grow field 1 from 1-byte to 2-byte varint; grow field 2's string.
	m2, err := m.Set(1, Value{Kind: KindVarint, U64: 300})
	if err != nil {
		t.Fatal(err)
	}
	m3, err := m2.Set(2, Value{Kind: KindString, Str: "longer"})
	if err != nil {
		t.Fatal(err)
	}
	want := concat(unknownA, refVarintField(1, 300), unknownB, refBytesField(2, []byte("longer")))
	if got := m3.Encode(); !bytes.Equal(got, want) {
		t.Fatalf("re-encode = %x, want %x", got, want)
	}
	// Untouched unknown segments are still the same shared subslices.
	segs := m3.Segments()
	if unsafe.SliceData(segs[0].Raw) != unsafe.SliceData(input) {
		t.Fatal("unknown segment A no longer shares input")
	}
	// Original message unchanged (immutability).
	if got := m.Encode(); !bytes.Equal(got, input) {
		t.Fatalf("original mutated: %x", got)
	}
}

func TestSingularConflictNeverLastWins(t *testing.T) {
	input := concat(refVarintField(1, 1), refVarintField(1, 2))
	_, _, err := Decode(input, schemaV1, Budget{})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Num != 1 {
		t.Fatalf("err = %v, want ConflictError for field 1", err)
	}
	// Repeated field 5 is fine.
	input = concat(refVarintField(5, 1), refVarintField(5, 2), refVarintField(5, 3))
	m, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(m.Segments()); n != 3 {
		t.Fatalf("repeated segments = %d", n)
	}
}

func TestUpgradeUnknownBecomesKnown(t *testing.T) {
	input := concat(refVarintField(1, 5), refVarintField(3, 77)) // 3 unknown under v1
	m1, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := m1.Upgrade(schemaV2, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := m2.Get(3)
	if !ok || v.Kind != KindVarint || v.U64 != 77 {
		t.Fatalf("upgraded field 3 = %+v", v)
	}
	if got := m2.Encode(); !bytes.Equal(got, input) {
		t.Fatalf("upgrade changed bytes: %x", got)
	}
}

func TestUpgradeConflictIsAtomic(t *testing.T) {
	input := concat(refVarintField(3, 1), refVarintField(1, 9), refVarintField(3, 2))
	m1, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	before := m1.Encode()
	_, err = m1.Upgrade(schemaV2, Budget{}) // field 3 singular in v2, occurs twice
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Num != 3 {
		t.Fatalf("err = %v, want ConflictError for field 3", err)
	}
	if got := m1.Encode(); !bytes.Equal(got, before) {
		t.Fatalf("failed upgrade mutated original: %x", got)
	}
}

func TestDowngradeKnownBecomesRaw(t *testing.T) {
	input := concat(refVarintField(1, 300), refBytesField(2, []byte("s")))
	m1, _, err := Decode(input, schemaV2, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	down, err := m1.Downgrade(&Schema{Fields: map[uint32]FieldSchema{2: {Kind: KindString}}}, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	segs := down.Segments()
	if segs[0].Known || !bytes.Equal(segs[0].Raw, refVarintField(1, 300)) {
		t.Fatalf("downgraded segment = %+v", segs[0])
	}
	if got := down.Encode(); !bytes.Equal(got, input) {
		t.Fatalf("downgrade changed bytes: %x", got)
	}
}

func TestOffsetsRecomputedAfterKnownFieldGrows(t *testing.T) {
	input := concat(
		refVarintField(1, 1),                 // 2 bytes
		refBytesField(6, []byte{0x01, 0x02}), // unknown, offset 2
		refVarintField(9, 5),                 // unknown, offset 6
	)
	m, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.Offsets(), []int{0, 2, 6}; !equalInts(got, want) {
		t.Fatalf("offsets = %v, want %v", got, want)
	}
	m2, err := m.Set(1, Value{Kind: KindVarint, U64: 0x4000}) // 1-byte -> 3-byte varint
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m2.Offsets(), []int{0, 4, 8}; !equalInts(got, want) {
		t.Fatalf("recomputed offsets = %v, want %v", got, want)
	}
	// Relative order and identity of unknown segments preserved.
	want := concat(refVarintField(1, 0x4000), refBytesField(6, []byte{0x01, 0x02}), refVarintField(9, 5))
	if got := m2.Encode(); !bytes.Equal(got, want) {
		t.Fatalf("re-encode = %x, want %x", got, want)
	}
}

func TestBudgetEnforced(t *testing.T) {
	deep := &Schema{}
	deep.Fields = map[uint32]FieldSchema{1: {Kind: KindMessage, Nested: deep}}
	nested := refBytesField(1, refBytesField(1, refBytesField(1, nil)))
	if _, _, err := Decode(nested, deep, Budget{MaxDepth: 2}); !errors.Is(err, ErrBudgetDepth) {
		t.Fatalf("depth err = %v", err)
	}
	many := concat(refVarintField(1, 1), refVarintField(5, 2), refVarintField(5, 3))
	if _, _, err := Decode(many, schemaV1, Budget{MaxFields: 2}); !errors.Is(err, ErrBudgetFields) {
		t.Fatalf("fields err = %v", err)
	}
	big := refBytesField(2, bytes.Repeat([]byte("x"), 100))
	if _, _, err := Decode(big, schemaV1, Budget{MaxValueBytes: 10}); !errors.Is(err, ErrBudgetBytes) {
		t.Fatalf("bytes err = %v", err)
	}
	// Budget accounting reports actual usage.
	_, u, err := Decode(big, schemaV1, Budget{})
	if err != nil || u.ValueBytes != 100 {
		t.Fatalf("usage = %+v, err = %v", u, err)
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	// Declared length 1<<40 with only 2 bytes available.
	input := concat(refTag(2, WireBytes), refVarint(1<<40), []byte{0x41, 0x42})
	if _, _, err := Decode(input, schemaV1, Budget{}); !errors.Is(err, ErrLength) {
		t.Fatalf("err = %v, want ErrLength", err)
	}
	// Same for an unknown field.
	input = concat(refTag(9, WireBytes), refVarint(1<<40), []byte{0x41})
	if _, _, err := Decode(input, schemaV1, Budget{}); !errors.Is(err, ErrLength) {
		t.Fatalf("err = %v, want ErrLength", err)
	}
}

func TestTruncatedAndMalformed(t *testing.T) {
	cases := [][]byte{
		{0x08},               // tag then missing varint value
		{0x0D, 0x01},         // fixed32 truncated
		{0x09, 0x01, 0x02},   // fixed64 truncated
		refTag(2, WireBytes), // missing length
		{0x00, 0x01},         // field number 0
		{0x0B},               // wire type 3 unsupported
	}
	for i, c := range cases {
		if _, _, err := Decode(c, schemaV1, Budget{}); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestFailureAtomicity(t *testing.T) {
	input := concat(refVarintField(1, 1), refBytesField(6, []byte{9}))
	m, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	before := m.Encode()
	if _, err := m.Set(1, Value{Kind: KindString, Str: "bad"}); err == nil {
		t.Fatal("kind mismatch accepted")
	}
	if _, err := m.Set(99, Value{Kind: KindVarint}); err == nil {
		t.Fatal("unknown field accepted by Set")
	}
	if got := m.Encode(); !bytes.Equal(got, before) {
		t.Fatalf("failed mutations changed message: %x", got)
	}
}

func TestCompactDetachesFromInput(t *testing.T) {
	input := concat(refBytesField(6, []byte{1, 2, 3}), refBytesField(2, []byte("ab")))
	m, _, err := Decode(input, schemaV1, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	c := m.Compact()
	input[2] = 0xFF // mutate the original buffer
	segs := c.Segments()
	if !bytes.Equal(segs[0].Raw, refBytesField(6, []byte{1, 2, 3})) {
		t.Fatalf("compact raw = %x", segs[0].Raw)
	}
	if v, _ := c.Get(2); v.Str != "ab" {
		t.Fatalf("string = %q", v.Str)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

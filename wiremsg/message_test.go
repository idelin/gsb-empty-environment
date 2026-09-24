package wiremsg

import (
	"bytes"
	"errors"
	"testing"
)

// Schemas used across tests. v1 knows only field 1; v2 (the "upgrade")
// additionally knows fields 2, 3 and 7.
var (
	subSchema = NewSchema(map[uint32]FieldDef{
		1: {Name: "id", Type: TypeUint64},
	})
	schemaV1 = NewSchema(map[uint32]FieldDef{
		1: {Name: "count", Type: TypeUint64},
	})
	schemaV2 = NewSchema(map[uint32]FieldDef{
		1: {Name: "count", Type: TypeUint64},
		2: {Name: "name", Type: TypeString},
		3: {Name: "sub", Type: TypeMessage, Sub: subSchema},
		7: {Name: "flag", Type: TypeUint64, Repeated: true},
	})
)

// reference is a hand-built byte-level reference message:
//
//	field 1 varint 150        08 96 01
//	field 2 string "hi"       12 02 68 69
//	field 7 varint 1          38 01            (unknown under v1)
//	field 7 varint 2          38 02            (repeated unknown under v1)
//	field 3 message {1: 1}    1a 02 08 01
//	field 9 bytes "\xff\x00"  4a 02 ff 00      (unknown, non-UTF8 payload)
func reference() []byte {
	return []byte{
		0x08, 0x96, 0x01,
		0x12, 0x02, 'h', 'i',
		0x38, 0x01,
		0x38, 0x02,
		0x1a, 0x02, 0x08, 0x01,
		0x4a, 0x02, 0xff, 0x00,
	}
}

func mustParse(t *testing.T, s *Schema, data []byte) *Message {
	t.Helper()
	m, err := Parse(s, data, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func mustMarshal(t *testing.T, m *Message) []byte {
	t.Helper()
	out, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return out
}

func TestRoundTripByteIdentical(t *testing.T) {
	for name, s := range map[string]*Schema{"v1": schemaV1, "v2": schemaV2, "nil": nil} {
		got := mustMarshal(t, mustParse(t, s, reference()))
		if !bytes.Equal(got, reference()) {
			t.Errorf("schema %s: round trip mismatch\n got %x\nwant %x", name, got, reference())
		}
	}
}

func TestKnownFieldsTyped(t *testing.T) {
	m := mustParse(t, schemaV2, reference())
	if got := m.Get(1); len(got) != 1 || got[0].Uint != 150 {
		t.Errorf("field 1 = %v, want 150", got)
	}
	if got := m.Get(2); len(got) != 1 || got[0].Str != "hi" {
		t.Errorf("field 2 = %v, want hi", got)
	}
	if got := m.Get(3); len(got) != 1 || got[0].Msg.Get(1)[0].Uint != 1 {
		t.Errorf("field 3 = %v, want nested id 1", got)
	}
	if got := m.Get(7); len(got) != 2 || got[0].Uint != 1 || got[1].Uint != 2 {
		t.Errorf("field 7 = %v, want [1 2]", got)
	}
}

func TestUnknownRawSharedWithInput(t *testing.T) {
	input := reference()
	m := mustParse(t, schemaV1, input)
	var unknowns []*Field
	for _, f := range m.Fields {
		if !f.Known {
			unknowns = append(unknowns, f)
		}
	}
	if len(unknowns) != 5 {
		t.Fatalf("got %d unknown fields, want 5", len(unknowns))
	}
	// Zero-copy: every raw slice must alias the input buffer.
	for _, f := range unknowns {
		if len(f.Raw) == 0 {
			t.Fatal("empty raw")
		}
		if &f.Raw[0] != &input[offsetOf(t, input, f.Raw)] {
			t.Errorf("field %d raw slice does not alias input", f.Number)
		}
	}
}

// offsetOf finds the exact offset of sub inside buf by pointer identity.
func offsetOf(t *testing.T, buf, sub []byte) int {
	t.Helper()
	for i := 0; i+len(sub) <= len(buf); i++ {
		if &buf[i] == &sub[0] {
			return i
		}
	}
	t.Fatalf("sub-slice %x not found in %x", sub, buf)
	return -1
}

func TestRepeatedUnknownNotOverwritten(t *testing.T) {
	m := mustParse(t, schemaV1, reference())
	var raws [][]byte
	for _, f := range m.Fields {
		if !f.Known && f.Number == 7 {
			raws = append(raws, f.Raw)
		}
	}
	if len(raws) != 2 {
		t.Fatalf("got %d copies of unknown field 7, want 2 (map would drop one)", len(raws))
	}
	if !bytes.Equal(raws[0], []byte{0x38, 0x01}) || !bytes.Equal(raws[1], []byte{0x38, 0x02}) {
		t.Errorf("raws = %x %x, want 3801 3802 in order", raws[0], raws[1])
	}
}

func TestUnknownNestedBytesPreservedVerbatim(t *testing.T) {
	// Unknown field 9 holds bytes that look like a nested message; under a
	// nil sub-schema they must stay opaque and byte-identical.
	m := mustParse(t, schemaV1, reference())
	var f *Field
	for _, fld := range m.Fields {
		if !fld.Known && fld.Number == 9 {
			f = fld
		}
	}
	if f == nil {
		t.Fatal("unknown field 9 missing")
	}
	if want := []byte{0x4a, 0x02, 0xff, 0x00}; !bytes.Equal(f.Raw, want) {
		t.Errorf("raw = %x, want %x", f.Raw, want)
	}
}

func TestKnownMutationRecomputesUnknownOffsets(t *testing.T) {
	m := mustParse(t, schemaV2, reference())
	// Grow field 1 from a 2-byte varint (150) to a 5-byte varint (1<<28),
	// and grow field 2 from 2 to 5 bytes. Every unknown byte after them
	// must shift and remain intact.
	if err := m.Set(1, Value{Uint: 1 << 28}); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(2, Value{Str: "hello"}); err != nil {
		t.Fatal(err)
	}
	got := mustMarshal(t, m)
	want := []byte{
		0x08, 0x80, 0x80, 0x80, 0x80, 0x01, // 1<<28 as varint
		0x12, 0x05, 'h', 'e', 'l', 'l', 'o',
		0x38, 0x01,
		0x38, 0x02,
		0x1a, 0x02, 0x08, 0x01,
		0x4a, 0x02, 0xff, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("after mutation\n got %x\nwant %x", got, want)
	}
	// Field identity: the *Field pointers for untouched entries are stable.
	// (Set replaced values in place; no list rebuild happened.)
}

func TestFieldIdentityStableAcrossMutation(t *testing.T) {
	m := mustParse(t, schemaV1, reference())
	before := make([]*Field, len(m.Fields))
	copy(before, m.Fields)
	if err := m.Set(1, Value{Uint: 7}); err != nil {
		t.Fatal(err)
	}
	if len(m.Fields) != len(before) {
		t.Fatal("field list length changed")
	}
	for i := range before {
		if m.Fields[i] != before[i] {
			t.Errorf("field %d identity changed", i)
		}
	}
}

func TestUpgradeUnknownBecomesKnown(t *testing.T) {
	data := reference()
	old := mustParse(t, schemaV1, data)
	if n := len(old.Get(7)); n != 0 {
		t.Fatalf("v1 should not type field 7, got %d values", n)
	}
	upgraded := mustParse(t, schemaV2, data)
	if got := upgraded.Get(7); len(got) != 2 || got[0].Uint != 1 || got[1].Uint != 2 {
		t.Fatalf("upgraded field 7 = %v, want [1 2]", got)
	}
	// Both encodings are byte-identical to the original.
	if !bytes.Equal(mustMarshal(t, old), data) || !bytes.Equal(mustMarshal(t, upgraded), data) {
		t.Error("upgrade changed the byte stream")
	}
}

func TestDowngradeKnownBecomesUnknown(t *testing.T) {
	// A writer on schema v2 produces bytes; a reader on v1 must preserve
	// the now-unknown fields verbatim and re-emit identical bytes.
	data := reference()
	m := mustParse(t, schemaV1, data)
	if err := m.Set(1, Value{Uint: 42}); err != nil {
		t.Fatal(err)
	}
	out := mustMarshal(t, m)
	// Byte-level reference: only the field-1 encoding changes.
	want := []byte{
		0x08, 0x2a,
		0x12, 0x02, 'h', 'i',
		0x38, 0x01,
		0x38, 0x02,
		0x1a, 0x02, 0x08, 0x01,
		0x4a, 0x02, 0xff, 0x00,
	}
	if !bytes.Equal(out, want) {
		t.Errorf("downgrade round trip\n got %x\nwant %x", out, want)
	}
}

func TestFieldOrderPreservedOnReorder(t *testing.T) {
	// Same fields, scrambled wire order: parse + marshal must reproduce the
	// exact scrambled order, not the schema order.
	data := []byte{
		0x4a, 0x02, 0xff, 0x00, // field 9 first
		0x38, 0x02, // field 7
		0x08, 0x96, 0x01, // field 1
		0x38, 0x01, // field 7 again
		0x12, 0x02, 'h', 'i', // field 2
	}
	if got := mustMarshal(t, mustParse(t, schemaV2, data)); !bytes.Equal(got, data) {
		t.Errorf("reordered round trip\n got %x\nwant %x", got, data)
	}
}

func TestConflictDuplicateNonRepeated(t *testing.T) {
	data := []byte{0x08, 0x01, 0x08, 0x02} // field 1 twice, not repeated
	m, err := Parse(schemaV1, data, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if m != nil {
		t.Error("message must be nil on conflict (no silent last-wins)")
	}
}

func TestConflictWireTypeMismatchAfterUpgrade(t *testing.T) {
	// Field 1 is a string on the wire but schemaV1 declares it varint: an
	// upgrade conflict that must surface, not be silently absorbed.
	data := []byte{0x0a, 0x01, 'x'}
	if _, err := Parse(schemaV1, data, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestTruncatedInputsFailAtomically(t *testing.T) {
	cases := map[string][]byte{
		"empty tag":         {0x80},
		"tag zero":          {0x00},
		"bad wire type":     {0x0b},
		"varint overflow":   {0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		"truncated fixed32": {0x0d, 0x01, 0x02},
		"truncated fixed64": {0x09, 0x01, 0x02, 0x03},
		"length overruns":   {0x12, 0x05, 'h', 'i'}, // declares 5, has 2
		"truncated len":     {0x12},
		"nested truncated":  {0x1a, 0x03, 0x08, 0x96}, // nested field cut off
	}
	for name, data := range cases {
		m, err := Parse(schemaV2, data, nil)
		if err == nil {
			t.Errorf("%s: expected error, got message %v", name, m)
		}
		if m != nil {
			t.Errorf("%s: partial message leaked on failure", name)
		}
	}
}

func TestFailedSetLeavesMessageUnchanged(t *testing.T) {
	m := mustParse(t, schemaV2, reference())
	before := mustMarshal(t, m)
	if err := m.Set(99, Value{Uint: 1}); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("Set unknown: %v, want ErrUnknownField", err)
	}
	if err := m.Set(3, Value{Msg: nil}); err == nil {
		t.Fatal("Set nil message should fail")
	}
	if after := mustMarshal(t, m); !bytes.Equal(before, after) {
		t.Error("failed Set mutated the message")
	}
}

func TestBudgetEnforced(t *testing.T) {
	data := reference()
	tight := NewBudget(Limits{MaxTotalBytes: 4, MaxFields: 100, MaxDepth: 32})
	if _, err := Parse(schemaV1, data, tight); !errors.Is(err, ErrBudget) {
		t.Errorf("byte budget: %v, want ErrBudget", err)
	}
	fewFields := NewBudget(Limits{MaxTotalBytes: 1 << 20, MaxFields: 2, MaxDepth: 32})
	if _, err := Parse(schemaV1, data, fewFields); !errors.Is(err, ErrBudget) {
		t.Errorf("field budget: %v, want ErrBudget", err)
	}
	shallow := NewBudget(Limits{MaxTotalBytes: 1 << 20, MaxFields: 100, MaxDepth: 0})
	if _, err := Parse(schemaV2, data, shallow); !errors.Is(err, ErrBudget) {
		t.Errorf("depth budget: %v, want ErrBudget", err)
	}
	// Budget accounting is observable.
	b := NewBudget(DefaultLimits)
	if _, err := Parse(schemaV1, data, b); err != nil {
		t.Fatal(err)
	}
	bytesUsed, fieldsUsed := b.Used()
	if bytesUsed != len(data) || fieldsUsed != 6 {
		t.Errorf("usage = (%d bytes, %d fields), want (%d, 6)", bytesUsed, fieldsUsed, len(data))
	}
}

func TestAppendRepeatedAndNewFieldOrder(t *testing.T) {
	m := mustParse(t, schemaV2, reference())
	if err := m.Append(7, Value{Uint: 3}); err != nil {
		t.Fatal(err)
	}
	got := mustMarshal(t, m)
	want := append(reference(), 0x38, 0x03) // appended at end, policy: end-of-list
	if !bytes.Equal(got, want) {
		t.Errorf("append order\n got %x\nwant %x", got, want)
	}
}

func TestAppendNonRepeatedConflict(t *testing.T) {
	m := mustParse(t, schemaV2, reference())
	if err := m.Append(1, Value{Uint: 9}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

package wireadmit

import (
	"bytes"
	"testing"
)

func TestNestingBudget(t *testing.T) {
	leaf, err := NewSchema([]Rule{{Number: 1, Type: Varint, Cardinality: Singular}})
	if err != nil {
		t.Fatalf("leaf schema: %v", err)
	}
	mid, err := NewSchema([]Rule{{Number: 2, Type: Message, Cardinality: Singular, Nested: leaf}})
	if err != nil {
		t.Fatalf("mid schema: %v", err)
	}
	top, err := NewSchema([]Rule{{Number: 3, Type: Message, Cardinality: Singular, Nested: mid}})
	if err != nil {
		t.Fatalf("top schema: %v", err)
	}

	// top(1) -> mid(2) -> leaf(3) -> varint field.
	msg := fieldBytes(3, fieldBytes(2, fieldVarint(1, 1)))

	rep, err := Admit(top, msg, WithNestingBudget(3))
	if err != nil {
		t.Fatalf("within-budget message rejected: %v", err)
	}
	if rep.MaxDepth != 3 {
		t.Fatalf("max depth = %d, want 3 (top, mid and leaf reports)", rep.MaxDepth)
	}

	_, err = Admit(top, msg, WithNestingBudget(2))
	assertCode(t, err, ErrNesting)

	// Unknown groups consume budget too: outer unknown group (depth 2)
	// containing an inner unknown group (depth 3) exceeds budget 2.
	schema := testSchema(t)
	var deepUnknownGroup []byte
	deepUnknownGroup = append(deepUnknownGroup, fieldTag(99, 3)...)
	deepUnknownGroup = append(deepUnknownGroup, fieldTag(99, 3)...)
	deepUnknownGroup = append(deepUnknownGroup, tag(1, 0), 1)
	deepUnknownGroup = append(deepUnknownGroup, fieldTag(99, 4)...)
	deepUnknownGroup = append(deepUnknownGroup, fieldTag(99, 4)...)
	_, err = Admit(schema, deepUnknownGroup, WithNestingBudget(2))
	assertCode(t, err, ErrNesting)
}

func TestPackedAndUnpackedMergeInArrivalOrder(t *testing.T) {
	schema := testSchema(t)

	// Field 4 (repeated varint): unpacked 10, packed [20,30], unpacked 40.
	packed40 := fieldBytes(4, append(varintBytes(20), varintBytes(30)...))
	msg := bytes.Join([][]byte{
		fieldVarint(4, 10),
		packed40,
		fieldVarint(4, 40),
	}, nil)

	rep, err := Admit(schema, msg)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if len(rep.Fields) != 3 {
		t.Fatalf("entries = %d, want 3", len(rep.Fields))
	}
	if rep.Fields[0].Packed || !rep.Fields[1].Packed || rep.Fields[2].Packed {
		t.Fatalf("packed flags wrong: %+v", rep.Fields)
	}

	vals := rep.Values(4)
	if len(vals) != 4 {
		t.Fatalf("merged values = %d, want 4", len(vals))
	}
	wantRaw := [][]byte{varintBytes(10), varintBytes(20), varintBytes(30), varintBytes(40)}
	for i, want := range wantRaw {
		if !bytes.Equal(vals[i].Raw, want) {
			t.Fatalf("value %d = %v, want %v", i, vals[i].Raw, want)
		}
	}

	// Mixed fixed32 packing with correct 4-byte alignment.
	packed32 := fieldBytes(5, append(fieldFixed32(0, 1)[1:], fieldFixed32(0, 2)[1:]...))
	rep2, err := Admit(schema, append(packed32, fieldFixed32(5, 3)...))
	if err != nil {
		t.Fatalf("fixed32 packed: %v", err)
	}
	if got := len(rep2.Values(5)); got != 3 {
		t.Fatalf("fixed32 merged count = %d, want 3", got)
	}

	// Empty packed payload is legal and contributes no values.
	empty := fieldBytes(4, nil)
	rep3, err := Admit(schema, empty)
	if err != nil {
		t.Fatalf("empty packed: %v", err)
	}
	if len(rep3.Values(4)) != 0 {
		t.Fatalf("empty packed produced values: %+v", rep3.Values(4))
	}
}

func TestPackedRejectedForNonPackable(t *testing.T) {
	schema := testSchema(t)

	// Singular varint may never use length-delimited (packed) encoding.
	_, err := Admit(schema, fieldBytes(1, varintBytes(1)))
	assertCode(t, err, ErrPacked)

	// Repeated bytes are length-delimited but not packable.
	if _, err := Admit(schema, fieldBytes(8, []byte{1, 2})); err != nil {
		t.Fatalf("ordinary repeated bytes rejected: %v", err)
	}

	// Repeated fixed32 packed payload whose body is not 4-byte aligned.
	bad := []byte{tag(5, 2), 3, 1, 2, 3}
	_, err = Admit(schema, bad)
	assertCode(t, err, ErrPacked)
}

func TestGroups(t *testing.T) {
	schema := testSchema(t)

	// Field 7 group { field 1 = 5 }.
	msg := []byte{tag(7, 3), tag(1, 0), 5, tag(7, 4)}
	rep, err := Admit(schema, msg)
	if err != nil {
		t.Fatalf("group admit: %v", err)
	}
	if len(rep.Fields) != 1 || rep.Fields[0].Number != 7 {
		t.Fatalf("group entry wrong: %+v", rep.Fields)
	}
	sub := rep.Fields[0].Values[0].Sub
	if sub == nil || len(sub.Fields) != 1 || !bytes.Equal(sub.Fields[0].Values[0].Raw, []byte{5}) {
		t.Fatalf("group sub-report wrong: %+v", sub)
	}

	// Duplicate singular field inside the group is rejected.
	dup := []byte{tag(7, 3), tag(1, 0), 5, tag(1, 0), 6, tag(7, 4)}
	_, err = Admit(schema, dup)
	assertCode(t, err, ErrDuplicateSingular)

	// Mismatched end-group field number.
	mismatch := []byte{tag(7, 3), tag(1, 0), 5, tag(8, 4)}
	_, err = Admit(schema, mismatch)
	assertCode(t, err, ErrInvalidField)

	// Unknown group with a nested unknown varint field is retained whole.
	unknown := append(append(append(
		fieldTag(99, 3),
		fieldTag(88, 0)...), 9),
		fieldTag(99, 4)...)
	rep2, err := Admit(schema, unknown)
	if err != nil {
		t.Fatalf("unknown group: %v", err)
	}
	if len(rep2.Unknown) != 1 || rep2.Unknown[0].End != len(unknown) {
		t.Fatalf("unknown group range wrong: %+v", rep2.Unknown)
	}
}

func TestReservedWireTypeAndZeroField(t *testing.T) {
	schema := testSchema(t)

	if _, err := Admit(schema, []byte{tag(1, 6), 0}); err == nil {
		t.Fatal("reserved wire type 6 accepted")
	} else {
		assertCode(t, err, ErrInvalidField)
	}
	if _, err := Admit(schema, []byte{tag(0, 0), 1}); err == nil {
		t.Fatal("field number zero accepted")
	} else {
		assertCode(t, err, ErrInvalidField)
	}
}

func TestStreamValidatorAcrossChunks(t *testing.T) {
	schema := testSchema(t)
	full := bytes.Join([][]byte{
		fieldVarint(1, 150),
		fieldBytes(3, []byte("payload")),
		fieldVarint(4, 7),
		fieldBytes(4, varintBytes(8)), // packed single-element field 4
	}, nil)

	// Feed one byte at a time so every tag, length and value is split.
	v := NewStreamValidator(schema)
	for i, b := range full {
		if n, err := v.Write([]byte{b}); err != nil || n != 1 {
			t.Fatalf("write %d: n=%d err=%v", i, n, err)
		}
	}
	if v.Len() != len(full) {
		t.Fatalf("buffered len = %d, want %d", v.Len(), len(full))
	}
	rep, err := v.Admit()
	if err != nil {
		t.Fatalf("stream admit: %v", err)
	}
	if len(rep.Fields) != 4 {
		t.Fatalf("stream fields = %d, want 4", len(rep.Fields))
	}
	if vals := rep.Values(4); len(vals) != 2 ||
		!bytes.Equal(vals[0].Raw, varintBytes(7)) ||
		!bytes.Equal(vals[1].Raw, varintBytes(8)) {
		t.Fatalf("stream merge order wrong: %+v", vals)
	}
	if !bytes.Equal(v.Bytes(), full) {
		t.Fatal("buffered bytes differ from original message")
	}

	// Reuse after Reset.
	v.Reset()
	if v.Len() != 0 {
		t.Fatal("reset did not clear buffer")
	}
	if _, err := v.Write(full); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, err := v.Admit(); err != nil {
		t.Fatalf("second admit: %v", err)
	}
}

func TestStreamTruncatedAndSizeLimit(t *testing.T) {
	schema := testSchema(t)

	// A split message whose declared length can never be satisfied must be
	// rejected at Admit, not mis-validated as chunks arrive.
	v := NewStreamValidator(schema)
	partial := fieldBytes(3, []byte("abc"))
	if _, err := v.Write(partial[:len(partial)-2]); err != nil {
		t.Fatalf("partial write: %v", err)
	}
	_, err := v.Admit()
	assertCode(t, err, ErrLengthOverflow)

	// Size limit is enforced on Write before the oversize chunk is buffered.
	small := NewStreamValidator(schema, WithMaxMessageSize(4))
	if _, err := small.Write([]byte{1, 2, 3}); err != nil {
		t.Fatalf("in-limit write: %v", err)
	}
	_, err = small.Write([]byte{4, 5})
	assertCode(t, err, ErrSize)
	if small.Len() != 3 {
		t.Fatalf("oversize write mutated buffer, len=%d", small.Len())
	}
}

func TestInputNotMutated(t *testing.T) {
	schema := testSchema(t)
	msg := append([]byte{}, bytes.Join([][]byte{
		fieldVarint(1, 150),
		fieldBytes(3, []byte("abc")),
		fieldBytes(4, varintBytes(9)),
	}, nil)...)
	backup := append([]byte{}, msg...)

	if _, err := Admit(schema, msg); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if !bytes.Equal(msg, backup) {
		t.Fatal("Admit mutated the input buffer")
	}

	// A second admission and the reported ranges still alias the same bytes.
	rep, err := Admit(schema, msg)
	if err != nil {
		t.Fatalf("second admit: %v", err)
	}
	for _, r := range rep.Unknown {
		if r.Start < 0 || r.End > len(msg) || r.Start >= r.End {
			t.Fatalf("invalid unknown range %+v", r)
		}
	}
}

func TestSchemaValidation(t *testing.T) {
	cases := map[string][]Rule{
		"zero field number":  {{Number: 0, Type: Varint}},
		"duplicate numbers":  {{Number: 1, Type: Varint}, {Number: 1, Type: Bytes}},
		"missing nested":     {{Number: 1, Type: Message}},
		"scalar with nested": {{Number: 1, Type: Varint, Nested: &Schema{}}},
		"unknown type":       {{Number: 1, Type: Type(99)}},
	}
	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSchema(rules); err == nil {
				t.Fatal("schema accepted")
			} else {
				assertCode(t, err, ErrSchema)
			}
		})
	}

	// Cyclic nested schema is rejected rather than overflowing the parser.
	child := &Schema{rules: map[int]*Rule{}}
	cycle := &Schema{rules: map[int]*Rule{
		1: {Number: 1, Type: Message, Cardinality: Singular, Nested: child},
	}}
	child.rules[1] = &Rule{Number: 1, Type: Message, Cardinality: Singular, Nested: cycle}
	if _, err := NewSchema([]Rule{{Number: 1, Type: Message, Nested: cycle}}); err == nil {
		t.Fatal("cyclic schema accepted")
	}
}

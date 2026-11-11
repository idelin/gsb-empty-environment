package wireadmit

import (
	"bytes"
	"errors"
	"testing"
)

func TestValidMessageAndOrdering(t *testing.T) {
	msg := bytes.Join([][]byte{
		fieldVarint(1, 150),
		fieldFixed64(2, 0x1122334455667788),
		fieldBytes(3, []byte("abc")),
	}, nil)

	rep, err := Admit(testSchema(t), msg)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if len(rep.Fields) != 3 {
		t.Fatalf("got %d fields, want 3", len(rep.Fields))
	}
	if rep.Fields[0].Number != 1 || rep.Fields[1].Number != 2 || rep.Fields[2].Number != 3 {
		t.Fatalf("field order not preserved: %+v", rep.Fields)
	}
	got := rep.Values(1)
	if len(got) != 1 || !bytes.Equal(got[0].Raw, varintBytes(150)) {
		t.Fatalf("field 1 raw mismatch: %+v", got)
	}
	if rep.MaxDepth != 1 {
		t.Fatalf("max depth = %d, want 1", rep.MaxDepth)
	}
}

func TestDuplicateSingularRejected(t *testing.T) {
	first := fieldVarint(1, 1)
	msg := append(first, fieldVarint(1, 2)...)
	_, err := Admit(testSchema(t), msg)
	assertCode(t, err, ErrDuplicateSingular)

	var e *Error
	errors.As(err, &e)
	if e.Field != 1 || e.Offset != len(first) {
		t.Fatalf("error context = field %d offset %d", e.Field, e.Offset)
	}
	if !errors.Is(err, ErrDuplicateField) {
		t.Fatalf("errors.Is sentinel failed: %v", err)
	}

	// An identical second occurrence is still a shadowing attempt.
	same := append(fieldVarint(1, 9), fieldVarint(1, 9)...)
	if _, err := Admit(testSchema(t), same); err == nil {
		t.Fatal("identical duplicate singular field was admitted")
	}
}

func TestWireTypeMismatchKnownField(t *testing.T) {
	schema := testSchema(t)
	cases := map[string][]byte{
		"varint field as fixed64":    {tag(1, 1), 0, 0, 0, 0, 0, 0, 0, 0},
		"fixed64 field as varint":    fieldVarint(2, 7),
		"bytes field as fixed32":     append([]byte{tag(3, 5)}, 1, 2, 3, 4),
		"fixed32 field as varint":    fieldVarint(5, 3),
		"message field as varint":    fieldVarint(6, 1),
		"group field as lengthdelim": fieldBytes(7, []byte{1}),
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Admit(schema, msg)
			assertCode(t, err, ErrWireType)
		})
	}

	// A scalar on wire type 2 means attempted packed encoding, which is
	// illegal for a singular field and reported as such.
	_, err := Admit(schema, fieldBytes(1, []byte{1}))
	assertCode(t, err, ErrPacked)
}

func TestUnknownFieldRetainedNotAuthorised(t *testing.T) {
	msg := append(fieldVarint(99, 42), fieldVarint(1, 7)...)
	rep, err := Admit(testSchema(t), msg)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if len(rep.Unknown) != 1 {
		t.Fatalf("unknown ranges = %d, want 1", len(rep.Unknown))
	}
	r := rep.Unknown[0]
	want := len(fieldVarint(99, 42))
	if r.Start != 0 || r.End != want {
		t.Fatalf("unknown range = [%d,%d), want [0,%d)", r.Start, r.End, want)
	}
	if !bytes.Equal(msg[r.Start:r.End], fieldVarint(99, 42)) {
		t.Fatal("unknown range does not point at original bytes")
	}
	if len(rep.Fields) != 1 || rep.Fields[0].Number != 1 {
		t.Fatalf("unknown field was interpreted as authorised: %+v", rep.Fields)
	}

	// Unknown length-delimited field whose payload contains a field number
	// that collides with an authorised field: payload stays opaque.
	trick := fieldBytes(99, fieldVarint(1, 123))
	rep2, err := Admit(testSchema(t), trick)
	if err != nil {
		t.Fatalf("admit trick: %v", err)
	}
	if len(rep2.Fields) != 0 || len(rep2.Unknown) != 1 {
		t.Fatalf("opaque payload leaked into schema: fields=%+v unknown=%+v", rep2.Fields, rep2.Unknown)
	}
}

func TestTruncatedInputs(t *testing.T) {
	schema := testSchema(t)
	cases := map[string][]byte{
		"tag varint cut":     {tag(1, 0) | 0x80},
		"varint value cut":   {tag(1, 0), 0x80},
		"fixed64 short":      {tag(2, 1), 1, 2, 3},
		"fixed32 short":      {tag(5, 5), 1, 2},
		"unterminated group": {tag(7, 3), tag(1, 0), 1},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Admit(schema, msg)
			assertCode(t, err, ErrTruncated)
		})
	}

	// Declared lengths that run past the buffer are length-overflow failures,
	// distinct from a cut varint or fixed-width payload.
	lengthCases := map[string][]byte{
		"length with no payload": {tag(3, 2), 5},
		"payload short":          {tag(3, 2), 5, 1, 2},
		"nested message short":   append([]byte{tag(6, 2), 3}, fieldVarint(1, 1)...),
	}
	for name, msg := range lengthCases {
		t.Run(name, func(t *testing.T) {
			_, err := Admit(schema, msg)
			assertCode(t, err, ErrLengthOverflow)
		})
	}

	// A packed body that fits its declared length but is misaligned is a
	// packed-encoding failure rather than truncation.
	_, err := Admit(schema, []byte{tag(5, 2), 5, 1, 2, 3, 4, 5})
	assertCode(t, err, ErrPacked)

	if _, err := Admit(schema, []byte{}); err != nil {
		t.Fatalf("empty message rejected: %v", err)
	}

	// Every prefix of a valid message must fail structurally or stay valid,
	// never panic or walk past the buffer.
	full := bytes.Join([][]byte{
		fieldVarint(1, 300),
		fieldBytes(3, []byte("hello")),
		fieldFixed64(2, 99),
	}, nil)
	for i := 0; i < len(full); i++ {
		if _, err := Admit(schema, full[:i]); err != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("prefix %d gave non-structured error %v", i, err)
			}
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	schema := testSchema(t)

	over10 := append([]byte{tag(1, 0)},
		0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02)
	_, err := Admit(schema, over10)
	assertCode(t, err, ErrVarintOverflow)

	over11 := append([]byte{tag(1, 0)},
		0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01)
	_, err = Admit(schema, over11)
	assertCode(t, err, ErrVarintOverflow)

	// Tenth byte == 1 is the maximum-width encoding (value 2^63).
	maxWidth := append([]byte{tag(1, 0)},
		0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01)
	if _, err := Admit(schema, maxWidth); err != nil {
		t.Fatalf("max-width varint rejected: %v", err)
	}

	// Overflow inside a packed payload surfaces as a packed-encoding error.
	badPacked := append([]byte{tag(4, 2), 11},
		0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01)
	_, err = Admit(schema, badPacked)
	assertCode(t, err, ErrPacked)
}

func TestLengthOverflow(t *testing.T) {
	msg := []byte{tag(3, 2), 0xac, 0x02, 1, 2}
	_, err := Admit(testSchema(t), msg)
	assertCode(t, err, ErrLengthOverflow)

	unknown := append(fieldTag(99, 2), 0xac, 0x02, 1, 2)
	_, err = Admit(testSchema(t), unknown)
	assertCode(t, err, ErrLengthOverflow)
}

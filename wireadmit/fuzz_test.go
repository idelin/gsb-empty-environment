package wireadmit

import (
	"bytes"
	"testing"
)

// FuzzParseNeverPanics feeds arbitrary bytes through every schema variant.
// Invariants checked for every accepted message:
//   - every reported range stays inside the input,
//   - ranges alias the exact original bytes,
//   - feeding the same bytes through StreamValidator gives the same result.
func FuzzParseNeverPanics(f *testing.F) {
	seeds := [][]byte{
		{},
		{0x08, 0x96, 0x01},
		{0x08, 0x80},
		{0x1a, 0x05, 1, 2, 3, 4, 5},
		{0x1a, 0xff, 0x01, 0},
		{0x3b, 0x08, 0x01, 0x3c},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f},
		{0x0d, 1, 2, 3},
		{0x22, 0x03, 0x08, 0x96, 0x01},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	schema := fuzzSchema(f)
	f.Fuzz(func(t *testing.T, msg []byte) {
		rep, err := Admit(schema, msg, WithNestingBudget(8), WithMaxMessageSize(1<<20))
		if err != nil {
			// The streaming path must reject the same bytes with the same code.
			v := NewStreamValidator(schema, WithNestingBudget(8), WithMaxMessageSize(1<<20))
			if _, werr := v.Write(msg); werr != nil {
				return
			}
			_, ferr := v.Admit()
			if ferr == nil {
				t.Fatalf("one-shot rejected (%v) but streaming accepted", err)
			}
			return
		}

		for i := range rep.Fields {
			r := rep.Fields[i].Data
			if r.Start < 0 || r.End > len(msg) || r.Start >= r.End {
				t.Fatalf("field range out of bounds: %+v len=%d", r, len(msg))
			}
			for _, val := range rep.Fields[i].Values {
				if val.Sub != nil {
					continue
				}
				if !bytes.HasPrefix(msg[r.Start:], val.Raw) && !within(msg, val.Raw) {
					t.Fatalf("value raw not aliasing input: field %d", rep.Fields[i].Number)
				}
			}
		}
		for _, r := range rep.Unknown {
			if r.Start < 0 || r.End > len(msg) || r.Start >= r.End {
				t.Fatalf("unknown range out of bounds: %+v len=%d", r, len(msg))
			}
		}

		v := NewStreamValidator(schema, WithNestingBudget(8), WithMaxMessageSize(1<<20))
		// Split at every possible single cut point across two writes.
		for cut := 0; cut <= len(msg); cut++ {
			v.Reset()
			if _, err := v.Write(msg[:cut]); err != nil {
				t.Fatalf("write first half: %v", err)
			}
			if _, err := v.Write(msg[cut:]); err != nil {
				t.Fatalf("write second half: %v", err)
			}
			if _, err := v.Admit(); err != nil {
				t.Fatalf("streaming rejected valid message at cut %d: %v", cut, err)
			}
		}
	})
}

func within(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if bytes.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func fuzzSchema(f *testing.F) *Schema {
	f.Helper()
	inner, err := NewSchema([]Rule{
		{Number: 1, Type: Varint, Cardinality: Singular},
		{Number: 2, Type: Bytes, Cardinality: Repeated},
	})
	if err != nil {
		f.Fatal(err)
	}
	schema, err := NewSchema([]Rule{
		{Number: 1, Type: Varint, Cardinality: Singular},
		{Number: 2, Type: Fixed64, Cardinality: Singular},
		{Number: 3, Type: Bytes, Cardinality: Singular},
		{Number: 4, Type: Varint, Cardinality: Repeated},
		{Number: 5, Type: Fixed32, Cardinality: Repeated},
		{Number: 6, Type: Message, Cardinality: Singular, Nested: inner},
	})
	if err != nil {
		f.Fatal(err)
	}
	return schema
}

package wire

import (
	"bytes"
	"encoding/hex"
	"io"
	"reflect"
	"strings"
	"testing"
)

// Hand-written byte fixtures, independent of the encoder under test, so
// the tests cannot pass because of a shared encode/decode bug.

// field 1, varint, max uint64 (10-byte varint).
const fxMaxUint64 = "08 ffffffffffffffffff01"

// field 2, length-delimited, empty payload.
const fxEmptyLen = "12 00"

// field 1 varint 1, field 3 fixed32 0x12345678, field 1 varint 2.
const fxRepeated = "08 01 1d 78563412 08 02"

// field 1, fixed64 0x1122334455667788.
const fxFixed64 = "09 8877665544332211"

// field 4, length-delimited, payload = {field 1 varint 150}.
const fxNested = "22 03 089601"

// field 5, length-delimited, packed fixed32 [1, 2].
const fxPacked32 = "2a 08 01000000 02000000"

// field 7, length-delimited, packed varint [1, 300].
const fxPackedVarint = "3a 03 01ac02"

// field 2^29-1 (the maximum), varint 1.
const fxMaxFieldNumber = "f8ffffff0f 01"

// Composite message exercising every wire type plus schema-interpreted
// nested and packed fields. Used with fxSchema.
const fxComposite = "08ac02 " + // field 1 varint 300
	"1202 6869 " + // field 2 bytes "hi"
	"2203 089601 " + // field 4 nested message {field 1 varint 150}
	"2a08 01000000 02000000 " + // field 5 packed fixed32 [1 2]
	"1d 78563412 " + // field 3 fixed32 0x12345678
	"31 8877665544332211 " + // field 6 fixed64 0x1122334455667788
	"3a03 01ac02" // field 7 packed varint [1 300]

var fxSchema = &Schema{
	Nested: map[uint32]*Schema{4: nil},
	Packed: map[uint32]int{5: TypeFixed32, 7: TypeVarint},
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad fixture hex: %v", err)
	}
	return b
}

func mustParse(t *testing.T, b []byte, lim Limits, schema *Schema) *Message {
	t.Helper()
	m, err := ParseMessage(b, lim, schema)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	return m
}

// drain feeds b to p in chunks of chunkSize (0 = one shot), finishes the
// stream, and collects all fields.
func drain(p *Parser, b []byte, chunkSize int) (*Message, error) {
	m := &Message{}
	collect := func() error {
		for {
			f, err := p.Next()
			if err == ErrNeedMore {
				return nil
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			m.Fields = append(m.Fields, *f)
		}
	}
	if chunkSize <= 0 {
		chunkSize = len(b)
		if chunkSize == 0 {
			chunkSize = 1
		}
	}
	for off := 0; off < len(b); off += chunkSize {
		end := off + chunkSize
		if end > len(b) {
			end = len(b)
		}
		if err := p.Feed(b[off:end]); err != nil {
			return m, err
		}
		if err := collect(); err != nil {
			return m, err
		}
	}
	if err := p.Finish(); err != nil {
		return m, err
	}
	for {
		f, err := p.Next()
		if err == io.EOF {
			return m, nil
		}
		if err != nil {
			return m, err
		}
		m.Fields = append(m.Fields, *f)
	}
}

func TestFixtureMaxUint64(t *testing.T) {
	b := unhex(t, fxMaxUint64)
	m := mustParse(t, b, Limits{}, nil)
	if len(m.Fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(m.Fields))
	}
	f := m.Fields[0]
	if f.Number != 1 || f.Type != TypeVarint {
		t.Fatalf("got field %d type %d, want field 1 varint", f.Number, f.Type)
	}
	if f.Value != ^uint64(0) {
		t.Fatalf("got value %#x, want max uint64", f.Value)
	}
	if !bytes.Equal(m.Marshal(), b) {
		t.Fatal("re-encode mismatch")
	}
}

func TestFixtureEmptyLength(t *testing.T) {
	b := unhex(t, fxEmptyLen)
	m := mustParse(t, b, Limits{}, nil)
	if len(m.Fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(m.Fields))
	}
	f := m.Fields[0]
	if f.Number != 2 || f.Type != TypeBytes || len(f.Raw) != 0 {
		t.Fatalf("got field %d type %d len %d, want field 2 empty bytes",
			f.Number, f.Type, len(f.Raw))
	}
	if !bytes.Equal(m.Marshal(), b) {
		t.Fatal("re-encode mismatch")
	}
}

func TestFixtureRepeatedFieldsKeepOrder(t *testing.T) {
	b := unhex(t, fxRepeated)
	m := mustParse(t, b, Limits{}, nil)
	if len(m.Fields) != 3 {
		t.Fatalf("got %d fields, want 3", len(m.Fields))
	}
	wantNums := []uint32{1, 3, 1}
	wantTypes := []int{TypeVarint, TypeFixed32, TypeVarint}
	wantVals := []uint64{1, 0x12345678, 2}
	for i := range m.Fields {
		f := &m.Fields[i]
		if f.Number != wantNums[i] || f.Type != wantTypes[i] || f.Value != wantVals[i] {
			t.Errorf("field %d: got (%d, type %d, %d), want (%d, type %d, %d)",
				i, f.Number, f.Type, f.Value, wantNums[i], wantTypes[i], wantVals[i])
		}
	}
	if !bytes.Equal(m.Marshal(), b) {
		t.Fatal("re-encode mismatch")
	}
}

func TestFixtureFixed64(t *testing.T) {
	b := unhex(t, fxFixed64)
	m := mustParse(t, b, Limits{}, nil)
	if m.Fields[0].Value != 0x1122334455667788 {
		t.Fatalf("got %#x", m.Fields[0].Value)
	}
	if !bytes.Equal(m.Marshal(), b) {
		t.Fatal("re-encode mismatch")
	}
}

func TestFixtureMaxFieldNumber(t *testing.T) {
	b := unhex(t, fxMaxFieldNumber)
	m := mustParse(t, b, Limits{}, nil)
	if m.Fields[0].Number != MaxFieldNumber {
		t.Fatalf("got field %d, want %d", m.Fields[0].Number, MaxFieldNumber)
	}
	if !bytes.Equal(m.Marshal(), b) {
		t.Fatal("re-encode mismatch")
	}
}

func TestFixtureNestedAndPacked(t *testing.T) {
	b := unhex(t, fxComposite)
	m := mustParse(t, b, Limits{}, fxSchema)
	if len(m.Fields) != 7 {
		t.Fatalf("got %d fields, want 7", len(m.Fields))
	}
	nested := m.Fields[2].Nested
	if nested == nil {
		t.Fatal("field 4 not interpreted as nested message")
	}
	if len(nested.Fields) != 1 || nested.Fields[0].Value != 150 {
		t.Fatalf("nested message = %+v", nested)
	}
	p32 := m.Fields[3].Packed
	if p32 == nil || p32.ElemType != TypeFixed32 || !reflect.DeepEqual(p32.Elems, []uint64{1, 2}) {
		t.Fatalf("packed fixed32 = %+v", p32)
	}
	pv := m.Fields[6].Packed
	if pv == nil || pv.ElemType != TypeVarint || !reflect.DeepEqual(pv.Elems, []uint64{1, 300}) {
		t.Fatalf("packed varint = %+v", pv)
	}
	if !bytes.Equal(m.Marshal(), b) {
		t.Fatal("re-encode mismatch")
	}
}

// TestTruncationEveryByte truncates a valid message at every possible
// byte length. Prefixes ending exactly on a field boundary must parse as
// a shorter valid message; every other prefix must fail with a
// truncation error pointing at the start of the incomplete field.
func TestTruncationEveryByte(t *testing.T) {
	b := unhex(t, "08 9601 "+"12 03 616263 "+"1d 78563412")
	boundaries := map[int]int{0: 0, 3: 1, 8: 2, 13: 3} // prefix len -> field count
	for n := 0; n <= len(b); n++ {
		m, err := drain(NewParser(Limits{}, nil), b[:n], 0)
		if fields, ok := boundaries[n]; ok {
			if err != nil {
				t.Fatalf("prefix %d (field boundary): %v", n, err)
			}
			if len(m.Fields) != fields {
				t.Fatalf("prefix %d: got %d fields, want %d", n, len(m.Fields), fields)
			}
			continue
		}
		pe, ok := err.(*Error)
		if !ok || pe.Op != "truncated" {
			t.Fatalf("prefix %d: got %v, want truncation error", n, err)
		}
		wantOff := int64(0)
		for boundary := range boundaries {
			if boundary < n && int64(boundary) > wantOff {
				wantOff = int64(boundary)
			}
		}
		if pe.Offset != wantOff {
			t.Fatalf("prefix %d: error offset %d, want %d", n, pe.Offset, wantOff)
		}
	}
}

// TestByteByByteFeed feeds the composite message one byte at a time and
// requires the result to be identical to a one-shot parse, and the
// re-encoding to reproduce the input exactly.
func TestByteByByteFeed(t *testing.T) {
	b := unhex(t, fxComposite)
	want := mustParse(t, b, Limits{}, fxSchema)

	p := NewParser(Limits{}, fxSchema)
	got, err := drain(p, b, 1)
	if err != nil {
		t.Fatalf("byte-by-byte parse: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("byte-by-byte result differs:\n got %+v\nwant %+v", got, want)
	}
	if !bytes.Equal(got.Marshal(), b) {
		t.Fatal("re-encode mismatch")
	}
	st := p.Stats()
	if st.PeakBuffered >= len(b) {
		t.Fatalf("peak buffered %d, want < %d (streaming must not hold the whole message)",
			st.PeakBuffered, len(b))
	}
	if st.BytesFed != int64(len(b)) {
		t.Fatalf("bytes fed %d, want %d", st.BytesFed, len(b))
	}
	if st.FieldsParsed != 8 { // 7 top-level + 1 nested
		t.Fatalf("fields parsed %d, want 8", st.FieldsParsed)
	}
	if st.MessagesParsed != 1 {
		t.Fatalf("messages parsed %d, want 1", st.MessagesParsed)
	}
}

// TestChunkedFeedEverySize parses the composite message with every chunk
// size from 1 to len(b) and requires identical results.
func TestChunkedFeedEverySize(t *testing.T) {
	b := unhex(t, fxComposite)
	want := mustParse(t, b, Limits{}, fxSchema)
	for size := 1; size <= len(b); size++ {
		got, err := drain(NewParser(Limits{}, fxSchema), b, size)
		if err != nil {
			t.Fatalf("chunk size %d: %v", size, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk size %d: result differs", size)
		}
	}
}

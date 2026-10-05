package wire

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
)

func decodeAll(t *testing.T, data []byte, limits Limits, schema Schema) (*Message, Stats, error) {
	t.Helper()
	return Decode(bytes.NewReader(data), limits, schema, 7) // small chunks on purpose
}

func mustDecode(t *testing.T, data []byte, schema Schema) *Message {
	t.Helper()
	m, _, err := decodeAll(t, data, Limits{}, schema)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	return m
}

func TestMaxUint64(t *testing.T) {
	m := mustDecode(t, fixtureMaxUint64, nil)
	if len(m.Fields) != 1 {
		t.Fatalf("want 1 field, got %d", len(m.Fields))
	}
	f := m.Fields[0]
	if f.Num != 1 || f.Wire != WireVarint || f.U64 != ^uint64(0) {
		t.Fatalf("got %+v, want field 1 varint max uint64", f)
	}
	if enc := m.Encode(); !bytes.Equal(enc, fixtureMaxUint64) {
		t.Fatalf("re-encode mismatch: %x", enc)
	}
}

func TestEmptyLengthField(t *testing.T) {
	m := mustDecode(t, fixtureEmptyLength, nil)
	f := m.Fields[0]
	if f.Num != 2 || f.Wire != WireBytes || f.Kind != KindRaw || len(f.Raw) != 0 {
		t.Fatalf("got %+v, want empty raw field 2", f)
	}
	if enc := m.Encode(); !bytes.Equal(enc, fixtureEmptyLength) {
		t.Fatalf("re-encode mismatch: %x", enc)
	}

	// Empty nested message and empty packed array.
	data := []byte{0x2a, 0x00, 0x32, 0x00}
	m = mustDecode(t, data, Schema{
		5: {Kind: KindMessage},
		6: {Kind: KindPacked, ElemWire: WireVarint},
	})
	if m.Fields[0].Kind != KindMessage || len(m.Fields[0].Msg.Fields) != 0 {
		t.Fatalf("empty nested message decoded wrong: %+v", m.Fields[0])
	}
	if m.Fields[1].Kind != KindPacked || len(m.Fields[1].Packed) != 0 {
		t.Fatalf("empty packed decoded wrong: %+v", m.Fields[1])
	}
	if enc := m.Encode(); !bytes.Equal(enc, data) {
		t.Fatalf("re-encode mismatch: %x", enc)
	}
}

func TestRepeatedFields(t *testing.T) {
	m := mustDecode(t, fixtureRepeated, nil)
	if len(m.Fields) != 3 {
		t.Fatalf("want 3 fields, got %d", len(m.Fields))
	}
	for i, f := range m.Fields {
		if f.Num != 1 || f.U64 != uint64(i+1) {
			t.Fatalf("field %d: got %+v", i, f)
		}
	}
	if enc := m.Encode(); !bytes.Equal(enc, fixtureRepeated) {
		t.Fatalf("re-encode mismatch: %x", enc)
	}
}

func TestMaxFieldNumber(t *testing.T) {
	m := mustDecode(t, fixtureMaxFieldNumber, nil)
	if m.Fields[0].Num != MaxFieldNumber {
		t.Fatalf("got field %d, want %d", m.Fields[0].Num, MaxFieldNumber)
	}
	if enc := m.Encode(); !bytes.Equal(enc, fixtureMaxFieldNumber) {
		t.Fatalf("re-encode mismatch: %x", enc)
	}
}

func TestCompositeDecode(t *testing.T) {
	m, stats, err := decodeAll(t, fixtureComposite, Limits{}, fixtureSchema)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(m.Fields) != 10 {
		t.Fatalf("want 10 top-level fields, got %d", len(m.Fields))
	}
	if m.Fields[0].U64 != 150 {
		t.Fatalf("field 1: got %d", m.Fields[0].U64)
	}
	if string(m.Fields[1].Raw) != "ab" {
		t.Fatalf("field 2: got %q", m.Fields[1].Raw)
	}
	if m.Fields[2].F64 != 1 {
		t.Fatalf("field 3: got %d", m.Fields[2].F64)
	}
	if m.Fields[3].F32 != 7 {
		t.Fatalf("field 4: got %d", m.Fields[3].F32)
	}
	inner := m.Fields[4].Msg
	if inner == nil || len(inner.Fields) != 2 || inner.Fields[0].U64 != 1 || inner.Fields[1].F32 != 0xdeadbeef {
		t.Fatalf("field 5 nested: got %+v", inner)
	}
	if !reflect.DeepEqual(m.Fields[5].Packed, []uint64{1, 2, 300}) {
		t.Fatalf("field 6 packed: got %v", m.Fields[5].Packed)
	}
	if !reflect.DeepEqual(m.Fields[6].Packed, []uint64{1, 2}) {
		t.Fatalf("field 7 packed: got %v", m.Fields[6].Packed)
	}
	if !reflect.DeepEqual(m.Fields[7].Packed, []uint64{5}) {
		t.Fatalf("field 8 packed: got %v", m.Fields[7].Packed)
	}
	if m.Fields[8].U64 != 9 {
		t.Fatalf("field 9 (unknown): got %+v", m.Fields[8])
	}
	if string(m.Fields[9].Raw) != "xyz" {
		t.Fatalf("field 10 (unknown): got %q", m.Fields[9].Raw)
	}
	if stats.Fields != 12 || stats.Messages != 1 || stats.BytesConsumed != int64(len(fixtureComposite)) {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestRoundTripPreservesUnknownFields(t *testing.T) {
	m := mustDecode(t, fixtureComposite, fixtureSchema)
	enc := m.Encode()
	if !bytes.Equal(enc, fixtureComposite) {
		t.Fatalf("re-encode dropped or altered data:\n got %x\nwant %x", enc, fixtureComposite)
	}
	// Re-decoding the re-encoded bytes must yield the same tree.
	m2 := mustDecode(t, enc, fixtureSchema)
	if !reflect.DeepEqual(m, m2) {
		t.Fatalf("re-decode mismatch:\n got %+v\nwant %+v", m2, m)
	}
}

func TestTruncationEveryByte(t *testing.T) {
	truncations := 0
	for i := 0; i < len(fixtureComposite); i++ {
		prefix := fixtureComposite[:i]
		m, _, err := decodeAll(t, prefix, Limits{}, fixtureSchema)
		if err == nil {
			// A prefix ending exactly on a top-level field boundary is a
			// valid shorter message; it must re-encode to itself.
			if enc := m.Encode(); !bytes.Equal(enc, prefix) {
				t.Fatalf("prefix %d: valid but re-encodes to %x", i, enc)
			}
			continue
		}
		var werr *Error
		if !errors.As(err, &werr) || werr.Code != ErrTruncated {
			t.Fatalf("prefix %d: got %v, want truncation error", i, err)
		}
		truncations++
	}
	if truncations == 0 {
		t.Fatal("expected at least one truncating prefix")
	}
	t.Logf("%d/%d prefixes correctly reported truncation", truncations, len(fixtureComposite))
}

func TestByteByByteFeeding(t *testing.T) {
	p := NewParser(Limits{}, fixtureSchema)
	tb := NewTreeBuilder()
	for i, b := range fixtureComposite {
		if err := p.Feed([]byte{b}); err != nil {
			t.Fatalf("byte %d: feed: %v", i, err)
		}
		for {
			ev, err := p.Next()
			if err == ErrNeedMore {
				break
			}
			if err != nil {
				t.Fatalf("byte %d: %v", i, err)
			}
			if err := tb.Apply(ev); err != nil {
				t.Fatalf("byte %d: %v", i, err)
			}
		}
	}
	p.Finish()
	for {
		ev, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		if err := tb.Apply(ev); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}
	want := mustDecode(t, fixtureComposite, fixtureSchema)
	if !reflect.DeepEqual(tb.Root(), want) {
		t.Fatalf("byte-by-byte tree mismatch:\n got %+v\nwant %+v", tb.Root(), want)
	}
	if enc := tb.Root().Encode(); !bytes.Equal(enc, fixtureComposite) {
		t.Fatalf("byte-by-byte re-encode mismatch: %x", enc)
	}
}

func TestOverflowVarint(t *testing.T) {
	cases := map[string][]byte{
		"tenth byte too large": {0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02},
		"eleven byte varint":   {0x08, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01},
		"overflow in key":      {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		"overflow in length":   {0x12, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := decodeAll(t, data, Limits{}, nil)
			var werr *Error
			if !errors.As(err, &werr) || werr.Code != ErrOverflow {
				t.Fatalf("got %v, want overflow error", err)
			}
		})
	}
}

func TestInvalidFieldNumber(t *testing.T) {
	cases := map[string][]byte{
		"field zero":      {0x00, 0x00},
		"field 2^29":      {0x80, 0x80, 0x80, 0x80, 0x10, 0x00},
		"field 2^29 wire": {0x87, 0x80, 0x80, 0x80, 0x10, 0x00},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := decodeAll(t, data, Limits{}, nil)
			var werr *Error
			if !errors.As(err, &werr) || werr.Code != ErrFieldNumber {
				t.Fatalf("got %v, want field-number error", err)
			}
		})
	}
}

func TestInvalidWireType(t *testing.T) {
	for _, wt := range []byte{3, 4, 6, 7} {
		data := []byte{0x08 | wt, 0x00}
		_, _, err := decodeAll(t, data, Limits{}, nil)
		var werr *Error
		if !errors.As(err, &werr) || werr.Code != ErrWireType {
			t.Fatalf("wire type %d: got %v, want wire-type error", wt, err)
		}
	}
}

func TestPackedFixedWidthNotDivisible(t *testing.T) {
	// field 7 = packed fixed32, but length 6 is not divisible by 4.
	data := []byte{0x3a, 0x06, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00}
	_, _, err := decodeAll(t, data, Limits{}, fixtureSchema)
	var werr *Error
	if !errors.As(err, &werr) || werr.Code != ErrPacked {
		t.Fatalf("got %v, want packed error", err)
	}
}

func TestPackedTruncatedElement(t *testing.T) {
	// field 6 = packed varint; last element starts but never terminates.
	data := []byte{0x32, 0x02, 0x01, 0xac}
	_, _, err := decodeAll(t, data, Limits{}, fixtureSchema)
	var werr *Error
	if !errors.As(err, &werr) || werr.Code != ErrPacked {
		t.Fatalf("got %v, want packed error", err)
	}
}

func TestLimits(t *testing.T) {
	t.Run("field length", func(t *testing.T) {
		_, _, err := decodeAll(t, fixtureComposite, Limits{MaxFieldLen: 1}, fixtureSchema)
		var werr *Error
		if !errors.As(err, &werr) || werr.Code != ErrLimit {
			t.Fatalf("got %v, want limit error", err)
		}
	})
	t.Run("field count", func(t *testing.T) {
		_, _, err := decodeAll(t, fixtureComposite, Limits{MaxFields: 5}, fixtureSchema)
		var werr *Error
		if !errors.As(err, &werr) || werr.Code != ErrLimit {
			t.Fatalf("got %v, want limit error", err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		if _, _, err := decodeAll(t, fixtureNested, Limits{MaxDepth: 3}, fixtureNestedSchema); err != nil {
			t.Fatalf("depth 3 should pass: %v", err)
		}
		_, _, err := decodeAll(t, fixtureNested, Limits{MaxDepth: 2}, fixtureNestedSchema)
		var werr *Error
		if !errors.As(err, &werr) || werr.Code != ErrLimit {
			t.Fatalf("got %v, want limit error", err)
		}
	})
	t.Run("total bytes", func(t *testing.T) {
		p := NewParser(Limits{MaxTotalBytes: 10}, nil)
		if err := p.Feed(fixtureComposite); err == nil {
			t.Fatal("want total-bytes limit error")
		} else {
			var werr *Error
			if !errors.As(err, &werr) || werr.Code != ErrLimit {
				t.Fatalf("got %v, want limit error", err)
			}
		}
	})
}

func TestErrorOffsetAndPath(t *testing.T) {
	// field 5 (nested) contains key 0x0f = field 1, wire type 7.
	data := []byte{0x2a, 0x02, 0x0f, 0x00}
	_, _, err := decodeAll(t, data, Limits{}, fixtureNestedSchema)
	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("got %v, want *Error", err)
	}
	if werr.Code != ErrWireType {
		t.Fatalf("got code %v, want wire-type", werr.Code)
	}
	if werr.Offset != 2 {
		t.Fatalf("got offset %d, want 2", werr.Offset)
	}
	if !reflect.DeepEqual(werr.Path, []int{5, 1}) {
		t.Fatalf("got path %v, want [5 1]", werr.Path)
	}
}

func TestNestedMessageEndsExactly(t *testing.T) {
	m := mustDecode(t, fixtureNested, fixtureNestedSchema)
	inner := m.Fields[0].Msg.Fields[0].Msg.Fields[0].Msg
	if inner.Fields[0].U64 != 1 {
		t.Fatalf("got %+v", inner)
	}
	if enc := m.Encode(); !bytes.Equal(enc, fixtureNested) {
		t.Fatalf("re-encode mismatch: %x", enc)
	}
}

func TestFeedAfterFinish(t *testing.T) {
	p := NewParser(Limits{}, nil)
	p.Finish()
	if err := p.Feed([]byte{0x08}); err == nil {
		t.Fatal("want error feeding after Finish")
	}
}

func TestEmptyMessage(t *testing.T) {
	m, _, err := decodeAll(t, nil, Limits{}, nil)
	if err != nil {
		t.Fatalf("empty input should decode: %v", err)
	}
	if len(m.Fields) != 0 {
		t.Fatalf("want no fields, got %d", len(m.Fields))
	}
}

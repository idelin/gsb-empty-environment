package message

import (
	"reflect"
	"testing"

	"pbufsim/wire"
)

// buildNested hand-encodes a 4-level deep message:
//
//	L1{ id=1:varint, child=2:L2{ child=1:L3{ child=1:L4{ val=1:zigzag } } } }
func buildNested(t *testing.T, depthVal int64) []byte {
	t.Helper()
	var l4 wire.Writer
	l4.AppendTag(1, wire.Varint)
	l4.AppendZigZag(depthVal)

	var l3 wire.Writer
	l3.AppendTag(1, wire.Bytes)
	l3.AppendBytes(l4.Bytes())

	var l2 wire.Writer
	l2.AppendTag(1, wire.Bytes)
	l2.AppendBytes(l3.Bytes())

	var l1 wire.Writer
	l1.AppendTag(1, wire.Varint)
	l1.AppendVarint(42)
	l1.AppendTag(2, wire.Bytes)
	l1.AppendBytes(l2.Bytes())
	return l1.Bytes()
}

func TestParseNestedDeep(t *testing.T) {
	buf := buildNested(t, -12345)

	m1, err := Parse(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := m1.Get(1); len(got) != 1 || got[0].Varint != 42 {
		t.Fatalf("L1.id: %+v", got)
	}
	m2, err := Nested(m1.Get(2)[0])
	if err != nil {
		t.Fatal(err)
	}
	m3, err := Nested(m2.Get(1)[0])
	if err != nil {
		t.Fatal(err)
	}
	m4, err := Nested(m3.Get(1)[0])
	if err != nil {
		t.Fatal(err)
	}
	got := wire.DecodeZigZag(m4.Get(1)[0].Varint)
	if got != -12345 {
		t.Fatalf("deepest value: got %d, want -12345", got)
	}
}

func TestParsePreservesOrderAndRepeats(t *testing.T) {
	var w wire.Writer
	w.AppendTag(2, wire.Varint)
	w.AppendVarint(20)
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(10)
	w.AppendTag(2, wire.Varint)
	w.AppendVarint(21)

	m, err := Parse(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Fields) != 3 || m.Fields[0].Num != 2 || m.Fields[1].Num != 1 || m.Fields[2].Num != 2 {
		t.Fatalf("order not preserved: %+v", m.Fields)
	}
	f2 := m.Get(2)
	if len(f2) != 2 || f2[0].Varint != 20 || f2[1].Varint != 21 {
		t.Fatalf("repeated field 2: %+v", f2)
	}
	if !m.Has(1) || m.Has(3) {
		t.Fatal("Has mismatch")
	}
}

func TestParseAllWireTypes(t *testing.T) {
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(150)
	w.AppendTag(2, wire.Fixed64)
	w.AppendFixed64(0xfeedfacecafef00d)
	w.AppendTag(3, wire.Bytes)
	w.AppendBytes([]byte("abc"))
	w.AppendTag(4, wire.Fixed32)
	w.AppendFixed32(0x0badf00d)

	m, err := Parse(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if m.Get(1)[0].Varint != 150 {
		t.Fatal("varint field")
	}
	if m.Get(2)[0].Fixed64 != 0xfeedfacecafef00d {
		t.Fatal("fixed64 field")
	}
	if string(m.Get(3)[0].Raw) != "abc" {
		t.Fatal("bytes field")
	}
	if m.Get(4)[0].Fixed32 != 0x0badf00d {
		t.Fatal("fixed32 field")
	}
}

func TestParseTruncatedAtEveryByte(t *testing.T) {
	// A single length-delimited field with a 20-byte payload: every
	// strict, non-empty prefix cuts either the tag, the length varint
	// or the payload, so every prefix must fail cleanly.
	var w wire.Writer
	w.AppendTag(1, wire.Bytes)
	w.AppendBytes([]byte("0123456789abcdefghij"))
	full := w.Bytes()
	for n := 1; n < len(full); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix of %d bytes: %v", n, r)
				}
			}()
			if _, err := Parse(full[:n]); err == nil {
				t.Fatalf("prefix of %d bytes parsed without error", n)
			}
		}()
	}
	if _, err := Parse(full); err != nil {
		t.Fatalf("full buffer: %v", err)
	}
}

func TestParseNeverPanicsOnPrefixes(t *testing.T) {
	// Fuzz-style guard: no prefix of a deeply nested message may panic,
	// regardless of whether it happens to be a valid shorter message.
	full := buildNested(t, 7)
	for n := 0; n <= len(full); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix of %d bytes: %v", n, r)
				}
			}()
			_, _ = Parse(full[:n])
		}()
	}
}

func TestParseEmpty(t *testing.T) {
	m, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Fields) != 0 {
		t.Fatal("empty input produced fields")
	}
}

func TestNestedOnNonBytesFails(t *testing.T) {
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(1)
	m, err := Parse(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Nested(m.Get(1)[0]); err == nil {
		t.Fatal("Nested on varint field succeeded")
	}
}

func TestUnpackVarints(t *testing.T) {
	var payload wire.Writer
	for _, v := range []uint64{0, 1, 300, 1 << 40} {
		payload.AppendVarint(v)
	}
	var w wire.Writer
	w.AppendTag(1, wire.Bytes)
	w.AppendBytes(payload.Bytes())

	m, err := Parse(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnpackVarints(m.Get(1)[0].Raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{0, 1, 300, 1 << 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnpackZigZags(t *testing.T) {
	var payload wire.Writer
	for _, v := range []int64{0, -1, 1, -300, 300} {
		payload.AppendZigZag(v)
	}
	got, err := UnpackZigZags(payload.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, -1, 1, -300, 300}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnpackFixed(t *testing.T) {
	var p32, p64 wire.Writer
	p32.AppendFixed32(11)
	p32.AppendFixed32(22)
	p64.AppendFixed64(111)
	p64.AppendFixed64(222)

	got32, err := UnpackFixed32s(p32.Bytes())
	if err != nil || !reflect.DeepEqual(got32, []uint32{11, 22}) {
		t.Fatalf("fixed32s: %v %v", got32, err)
	}
	got64, err := UnpackFixed64s(p64.Bytes())
	if err != nil || !reflect.DeepEqual(got64, []uint64{111, 222}) {
		t.Fatalf("fixed64s: %v %v", got64, err)
	}
}

func TestUnpackTruncated(t *testing.T) {
	// Packed varint whose last element is cut off.
	if _, err := UnpackVarints([]byte{0x96}); !wire.IsTruncated(err) {
		t.Fatalf("packed varints: want truncation, got %v", err)
	}
	// Packed fixed32 with 5 bytes (not a multiple of 4).
	if _, err := UnpackFixed32s([]byte{1, 2, 3, 4, 5}); !wire.IsTruncated(err) {
		t.Fatalf("packed fixed32: want truncation, got %v", err)
	}
	// Packed fixed64 with 9 bytes.
	if _, err := UnpackFixed64s(make([]byte, 9)); !wire.IsTruncated(err) {
		t.Fatalf("packed fixed64: want truncation, got %v", err)
	}
	// Packed zigzag cut off.
	if _, err := UnpackZigZags([]byte{0x80}); !wire.IsTruncated(err) {
		t.Fatalf("packed zigzags: want truncation, got %v", err)
	}
}

func TestParseBadTag(t *testing.T) {
	// Field number 0.
	if _, err := Parse([]byte{0x00, 0x01}); err == nil {
		t.Fatal("field number 0 accepted")
	}
	// Unknown wire type 7.
	if _, err := Parse([]byte{0x0f, 0x01}); err == nil {
		t.Fatal("wire type 7 accepted")
	}
}

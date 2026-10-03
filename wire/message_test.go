package wire

import (
	"reflect"
	"strings"
	"testing"
)

// buildSampleStream hand-crafts a multi-level nested message with
// packed repeated fields. Layout: field1 varint 150; field2 zigzag
// sint64 -2; field3 fixed64; field4 embedded message containing
// field1 varint 7 and field2 a second-level embedded message with
// field1 varint 42; field5 packed varint [1,2,300]; field6 packed
// fixed32 [10,20]; field7 packed zigzag [-1,0,1].
func buildSampleStream() []byte {
	var buf []byte
	buf = AppendTag(buf, 1, Varint)
	buf = AppendVarint(buf, 150)
	buf = AppendTag(buf, 2, Varint)
	buf = AppendVarint(buf, ZigzagEncode(-2))
	buf = AppendTag(buf, 3, Fixed64)
	buf = AppendFixed64(buf, 0x0102030405060708)

	var inner2 []byte
	inner2 = AppendTag(inner2, 1, Varint)
	inner2 = AppendVarint(inner2, 42)
	var inner1 []byte
	inner1 = AppendTag(inner1, 1, Varint)
	inner1 = AppendVarint(inner1, 7)
	inner1 = AppendTag(inner1, 2, Bytes)
	inner1 = AppendVarint(inner1, uint64(len(inner2)))
	inner1 = append(inner1, inner2...)
	buf = AppendTag(buf, 4, Bytes)
	buf = AppendVarint(buf, uint64(len(inner1)))
	buf = append(buf, inner1...)

	var packed []byte
	for _, v := range []uint64{1, 2, 300} {
		packed = AppendVarint(packed, v)
	}
	buf = AppendTag(buf, 5, Bytes)
	buf = AppendVarint(buf, uint64(len(packed)))
	buf = append(buf, packed...)

	var packed32 []byte
	packed32 = AppendFixed32(packed32, 10)
	packed32 = AppendFixed32(packed32, 20)
	buf = AppendTag(buf, 6, Bytes)
	buf = AppendVarint(buf, uint64(len(packed32)))
	buf = append(buf, packed32...)

	var packedZ []byte
	for _, v := range []int64{-1, 0, 1} {
		packedZ = AppendVarint(packedZ, ZigzagEncode(v))
	}
	buf = AppendTag(buf, 7, Bytes)
	buf = AppendVarint(buf, uint64(len(packedZ)))
	buf = append(buf, packedZ...)
	return buf
}

func TestParseNestedAndPacked(t *testing.T) {
	d := NewDecoder()
	m, err := d.Parse(buildSampleStream())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, ok := m.Last(1); !ok || v.Num != 150 {
		t.Fatalf("field 1 = %+v ok=%v", v, ok)
	}
	if v, ok := m.Last(2); !ok || v.AsZigzag() != -2 {
		t.Fatalf("field 2 = %+v ok=%v", v, ok)
	}
	if v, ok := m.Last(3); !ok || v.Fixed64 != 0x0102030405060708 {
		t.Fatalf("field 3 = %+v ok=%v", v, ok)
	}
	n1v, ok := m.Last(4)
	if !ok {
		t.Fatal("field 4 missing")
	}
	n1, err := d.Nested(n1v, 0)
	if err != nil {
		t.Fatalf("Nested depth1: %v", err)
	}
	if v, _ := n1.Last(1); v.Num != 7 {
		t.Fatalf("nested field 1 = %+v", v)
	}
	n2v, ok := n1.Last(2)
	if !ok {
		t.Fatal("nested field 2 missing")
	}
	n2, err := d.Nested(n2v, 1)
	if err != nil {
		t.Fatalf("Nested depth2: %v", err)
	}
	if v, _ := n2.Last(1); v.Num != 42 {
		t.Fatalf("depth2 field 1 = %+v", v)
	}
	pv, _ := m.Last(5)
	us, err := DecodePackedVarint(pv.Bytes)
	if err != nil || !reflect.DeepEqual(us, []uint64{1, 2, 300}) {
		t.Fatalf("packed varint = %v err=%v", us, err)
	}
	p32, _ := m.Last(6)
	f32, err := DecodePackedFixed32(p32.Bytes)
	if err != nil || !reflect.DeepEqual(f32, []uint32{10, 20}) {
		t.Fatalf("packed fixed32 = %v err=%v", f32, err)
	}
	pz, _ := m.Last(7)
	zs, err := DecodePackedZigzag(pz.Bytes)
	if err != nil || !reflect.DeepEqual(zs, []int64{-1, 0, 1}) {
		t.Fatalf("packed zigzag = %v err=%v", zs, err)
	}
}

func TestTruncationSafetyEveryOffset(t *testing.T) {
	stream := buildSampleStream()
	d := NewDecoder()
	for i := 0; i < len(stream); i++ {
		// Must never panic on any prefix of the stream.
		_, _ = d.Parse(stream[:i])
	}
	// Cutting inside the final packed payload must be reported as
	// a truncation error with field context.
	cut := len(stream) - 1
	if _, err := d.Parse(stream[:cut]); !IsTruncated(err) {
		t.Fatalf("cut at %d: want truncation, got %v", cut, err)
	}
}

func TestTruncatedLengthDelimited(t *testing.T) {
	var buf []byte
	buf = AppendTag(buf, 4, Bytes)
	buf = AppendVarint(buf, 100) // declares 100 bytes, provides 3
	buf = append(buf, 0x08, 0x96, 0x01)
	_, err := NewDecoder().Parse(buf)
	if !IsTruncated(err) {
		t.Fatalf("want truncation, got %v", err)
	}
	fe, ok := err.(*FieldError)
	if !ok {
		t.Fatalf("want FieldError, got %T", err)
	}
	if !strings.Contains(fe.Error(), "field 4") {
		t.Fatalf("error should name field 4: %v", fe)
	}
}

func TestDepthLimit(t *testing.T) {
	payload := []byte{0x08, 0x01} // field 1 varint 1
	for i := 0; i < 5; i++ {
		var wrap []byte
		wrap = AppendTag(wrap, 1, Bytes)
		wrap = AppendVarint(wrap, uint64(len(payload)))
		payload = append(wrap, payload...)
	}
	d := &Decoder{MaxDepth: 3, MaxLen: DefaultMaxLen}
	m, err := d.Parse(payload)
	if err != nil {
		t.Fatalf("top parse: %v", err)
	}
	depth := 0
	cur := m
	for {
		v, ok := cur.Last(1)
		if !ok || v.WireType != Bytes {
			break
		}
		next, err := d.Nested(v, depth)
		if err != nil {
			if depth < 3 {
				t.Fatalf("depth %d within limit but failed: %v", depth, err)
			}
			return // limit enforced
		}
		cur = next
		depth++
	}
	t.Fatal("depth limit was not enforced")
}

func TestBadInputs(t *testing.T) {
	d := NewDecoder()
	var big []byte
	big = AppendTag(big, 1, Bytes)
	big = AppendVarint(big, uint64(DefaultMaxLen)+1)
	if _, err := d.Parse(big); err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("oversized length: got %v", err)
	}
	if _, err := DecodePackedFixed32([]byte{1, 2, 3}); !IsTruncated(err) {
		t.Fatalf("packed fixed32 dangling: got %v", err)
	}
	if _, err := DecodePackedVarint([]byte{0x80}); !IsTruncated(err) {
		t.Fatalf("packed varint truncated: got %v", err)
	}
	if _, err := d.Nested(Value{WireType: Varint, Num: 1}, 0); err == nil {
		t.Fatal("Nested on varint should fail")
	}
}

package proto

import (
	"reflect"
	"strings"
	"testing"

	"protowire/wire"
)

// buildAddress encodes: message Address { string city = 1; sint32 zip = 2; }
func buildAddress(city string, zip int32) []byte {
	w := wire.NewWriter()
	w.AppendVarint(EncodeTag(1, WireLengthDelimited))
	w.AppendLengthDelimited([]byte(city))
	w.AppendVarint(EncodeTag(2, WireVarint))
	w.AppendZigZag32(zip)
	return w.Bytes()
}

// buildPerson encodes a 3-level nested message with a packed array:
//
//	message Person {
//	  string name        = 1;
//	  Address addr       = 2;   // nested (city, zip)
//	  repeated int32 ids = 3 [packed=true];
//	  fixed64 token      = 4;
//	  sint64 balance     = 5;
//	}
func buildPerson() []byte {
	addr := buildAddress("Shanghai", -200001)

	packed := wire.NewWriter()
	for _, id := range []int64{7, -8, 9000} {
		packed.AppendZigZag64(id)
	}

	w := wire.NewWriter()
	w.AppendVarint(EncodeTag(1, WireLengthDelimited))
	w.AppendLengthDelimited([]byte("alice"))
	w.AppendVarint(EncodeTag(2, WireLengthDelimited))
	w.AppendLengthDelimited(addr)
	w.AppendVarint(EncodeTag(3, WireLengthDelimited))
	w.AppendLengthDelimited(packed.Bytes())
	w.AppendVarint(EncodeTag(4, WireFixed64))
	w.AppendFixed64(0xfeedfacecafebeef)
	w.AppendVarint(EncodeTag(5, WireVarint))
	w.AppendZigZag64(-123456789)
	return w.Bytes()
}

func TestParseNestedMessage(t *testing.T) {
	msg, err := Parse(buildPerson())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msg) != 5 {
		t.Fatalf("got %d fields want 5", len(msg))
	}

	if got := string(msg[0].Bytes); got != "alice" {
		t.Fatalf("name = %q", got)
	}

	// Level 2: address.
	addr, err := msg[1].ParseNested(0)
	if err != nil {
		t.Fatalf("ParseNested: %v", err)
	}
	if got := string(addr[0].Bytes); got != "Shanghai" {
		t.Fatalf("city = %q", got)
	}
	if got := addr[1].AsZigZag32(); got != -200001 {
		t.Fatalf("zip = %d", got)
	}

	// Packed repeated ids.
	ids, err := PackedZigZag64(msg[2].Bytes)
	if err != nil {
		t.Fatalf("PackedZigZag64: %v", err)
	}
	if want := []int64{7, -8, 9000}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v want %v", ids, want)
	}

	if msg[3].WireType != WireFixed64 || msg[3].U64 != 0xfeedfacecafebeef {
		t.Fatalf("token = %#x", msg[3].U64)
	}
	if got := msg[4].AsZigZag64(); got != -123456789 {
		t.Fatalf("balance = %d", got)
	}
}

func TestParseThreeLevelNesting(t *testing.T) {
	inner := wire.NewWriter() // { fixed32 deep = 1; }
	inner.AppendVarint(EncodeTag(1, WireFixed32))
	inner.AppendFixed32(424242)

	mid := wire.NewWriter() // { bytes inner = 1; }
	mid.AppendVarint(EncodeTag(1, WireLengthDelimited))
	mid.AppendLengthDelimited(inner.Bytes())

	outer := wire.NewWriter() // { bytes mid = 1; }
	outer.AppendVarint(EncodeTag(1, WireLengthDelimited))
	outer.AppendLengthDelimited(mid.Bytes())

	m1, err := Parse(outer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	m2, err := m1[0].ParseNested(0)
	if err != nil {
		t.Fatal(err)
	}
	m3, err := m2[0].ParseNested(1)
	if err != nil {
		t.Fatal(err)
	}
	if m3[0].WireType != WireFixed32 || m3[0].U64 != 424242 {
		t.Fatalf("deep field = %+v", m3[0])
	}
}

func TestParseTruncatedField(t *testing.T) {
	full := buildPerson()
	// Cut the stream in the middle of the last field's varint payload.
	trunc := full[:len(full)-3]
	_, err := Parse(trunc)
	if err == nil {
		t.Fatal("expected truncation error")
	}
	if !strings.Contains(err.Error(), "truncated") && !wire.IsTruncated(err) {
		t.Fatalf("error %q does not report truncation", err)
	}
}

func TestParseCorruptLengthPrefix(t *testing.T) {
	// Field 1 claims 9999 bytes of payload; only 1 byte follows.
	w := wire.NewWriter()
	w.AppendVarint(EncodeTag(1, WireLengthDelimited))
	w.AppendVarint(9999)
	w.AppendBytes([]byte{0x00})
	_, err := Parse(w.Bytes())
	if err == nil {
		t.Fatal("expected truncation error")
	}
	if !strings.Contains(err.Error(), "exceeds remaining") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseBadWireType(t *testing.T) {
	for _, wt := range []int{WireStartGroup, WireEndGroup, 6, 7} {
		w := wire.NewWriter()
		w.AppendVarint(EncodeTag(1, wt))
		w.AppendVarint(0)
		if _, err := Parse(w.Bytes()); err == nil {
			t.Fatalf("wire type %d: expected error", wt)
		}
	}
	// Field number 0 is illegal.
	w := wire.NewWriter()
	w.AppendVarint(EncodeTag(0, WireVarint))
	w.AppendVarint(1)
	if _, err := Parse(w.Bytes()); err == nil {
		t.Fatal("field number 0: expected error")
	}
}

func TestParseDepthLimit(t *testing.T) {
	// Build MaxDepth+2 nested length-delimited layers.
	payload := []byte{0x08, 0x01} // innermost: field 1 varint 1
	for i := 0; i < MaxDepth+2; i++ {
		w := wire.NewWriter()
		w.AppendVarint(EncodeTag(1, WireLengthDelimited))
		w.AppendLengthDelimited(payload)
		payload = w.Bytes()
	}
	// Walk down manually; must hit the depth guard, not a stack overflow.
	m, err := Parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	depth := 0
	for {
		var next Message
		next, err = m[0].ParseNested(depth)
		if err != nil {
			break
		}
		m = next
		depth++
	}
	if err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("expected depth guard, got %v (depth %d)", err, depth)
	}
}

func TestPackedFixedWidths(t *testing.T) {
	w32 := wire.NewWriter()
	w32.AppendFixed32(1)
	w32.AppendFixed32(0xffffffff)
	got32, err := PackedFixed32(w32.Bytes())
	if err != nil || !reflect.DeepEqual(got32, []uint32{1, 0xffffffff}) {
		t.Fatalf("fixed32 = %v err=%v", got32, err)
	}
	if _, err := PackedFixed32([]byte{1, 2, 3}); err == nil {
		t.Fatal("fixed32: expected length error")
	}

	w64 := wire.NewWriter()
	w64.AppendFixed64(0x1122334455667788)
	got64, err := PackedFixed64(w64.Bytes())
	if err != nil || !reflect.DeepEqual(got64, []uint64{0x1122334455667788}) {
		t.Fatalf("fixed64 = %v err=%v", got64, err)
	}
	if _, err := PackedFixed64([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("fixed64: expected length error")
	}
}

func TestPackedVarintsTruncated(t *testing.T) {
	// Valid varint then a dangling continuation byte.
	if _, err := PackedVarints([]byte{0x01, 0x80}); err != wire.ErrTruncated {
		t.Fatalf("got %v want ErrTruncated", err)
	}
}

func TestMessageByNum(t *testing.T) {
	w := wire.NewWriter()
	for _, v := range []uint64{10, 20, 30} {
		w.AppendVarint(EncodeTag(2, WireVarint))
		w.AppendVarint(v)
	}
	w.AppendVarint(EncodeTag(1, WireVarint))
	w.AppendVarint(99)
	msg, err := Parse(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	fs := msg.ByNum(2)
	if len(fs) != 3 || fs[0].U64 != 10 || fs[2].U64 != 30 {
		t.Fatalf("ByNum(2) = %v", fs)
	}
	if got := msg.ByNum(1); len(got) != 1 || got[0].U64 != 99 {
		t.Fatalf("ByNum(1) = %v", got)
	}
}

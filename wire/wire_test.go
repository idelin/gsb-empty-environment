package wire

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1 << 21, 1 << 35, 1 << 63, math.MaxUint64}
	for _, want := range cases {
		e := NewEncoder()
		e.AppendVarint(want)
		got, err := NewDecoder(e.Bytes()).ReadVarint()
		if err != nil {
			t.Fatalf("varint(%d): %v", want, err)
		}
		if got != want {
			t.Fatalf("varint round trip: got %d, want %d", got, want)
		}
	}
}

func TestVarintKnownEncodings(t *testing.T) {
	// 1 -> 0x01, 300 -> 0xAC 0x02 (7-bit groups, LSB first)
	e := NewEncoder()
	e.AppendVarint(300)
	if !bytes.Equal(e.Bytes(), []byte{0xAC, 0x02}) {
		t.Fatalf("300 encoded as %x, want ac02", e.Bytes())
	}
}

func TestVarintTruncated(t *testing.T) {
	// Continuation bit set but buffer ends.
	d := NewDecoder([]byte{0x80})
	if _, err := d.ReadVarint(); err != ErrTruncated {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: exceeds MaxVarintBytes.
	d := NewDecoder([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01})
	if _, err := d.ReadVarint(); err != ErrOverflow {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
	// 10th byte carrying more than 1 payload bit.
	d = NewDecoder([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	if _, err := d.ReadVarint(); err != ErrOverflow {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		in   int64
		want uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := EncodeZigZag(c.in); got != c.want {
			t.Fatalf("EncodeZigZag(%d) = %d, want %d", c.in, got, c.want)
		}
		e := NewEncoder()
		e.AppendVarint(c.want)
		got, err := NewDecoder(e.Bytes()).ReadZigZag()
		if err != nil || got != c.in {
			t.Fatalf("ReadZigZag(%d) = %d, %v; want %d", c.want, got, err, c.in)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	e := NewEncoder()
	e.AppendFixed32(0xdeadbeef)
	e.AppendFixed64(0x0102030405060708)
	d := NewDecoder(e.Bytes())
	u32, err := d.ReadFixed32()
	if err != nil || u32 != 0xdeadbeef {
		t.Fatalf("fixed32 = %x, %v", u32, err)
	}
	u64, err := d.ReadFixed64()
	if err != nil || u64 != 0x0102030405060708 {
		t.Fatalf("fixed64 = %x, %v", u64, err)
	}
	if !d.EOF() {
		t.Fatal("decoder not exhausted")
	}
}

func TestFixedTruncated(t *testing.T) {
	if _, err := NewDecoder([]byte{1, 2, 3}).ReadFixed32(); err != ErrTruncated {
		t.Fatalf("fixed32: got %v, want ErrTruncated", err)
	}
	if _, err := NewDecoder([]byte{1, 2, 3, 4, 5, 6, 7}).ReadFixed64(); err != ErrTruncated {
		t.Fatalf("fixed64: got %v, want ErrTruncated", err)
	}
}

func TestLengthDelimitedTruncated(t *testing.T) {
	// Length prefix says 5, only 2 bytes follow.
	if _, err := NewDecoder([]byte{0x05, 0xaa, 0xbb}).ReadBytes(); err != ErrTruncated {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestParseTruncatedField(t *testing.T) {
	// Field 1 varint = 150, then field 2 length-delimited claiming 10 bytes
	// but the buffer is cut short: safe bounds guard must fire.
	e := NewEncoder()
	e.AddVarintField(1, 150)
	e.AppendTag(2, WireLengthDelimited)
	e.AppendVarint(10)
	e.AppendBytes([]byte{1, 2, 3}) // only 3 of 10
	_, err := Parse(e.Bytes())
	if err == nil {
		t.Fatal("expected truncation error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte(ErrTruncated.Error())) {
		t.Fatalf("error %q does not report bounds guard", err)
	}
}

func TestParseBadTagAndGroups(t *testing.T) {
	// Field number 0 is illegal.
	if _, err := Parse([]byte{0x00, 0x01}); err == nil ||
		!bytes.Contains([]byte(err.Error()), []byte(ErrBadTag.Error())) {
		t.Fatalf("tag 0: got %v, want ErrBadTag", err)
	}
	// Deprecated group wire types must be rejected.
	if _, err := Parse([]byte{0x0b}); err == nil { // field 1, wire type 3
		t.Fatal("start-group wire type accepted")
	}
	if _, err := Parse([]byte{0x0c}); err == nil { // field 1, wire type 4
		t.Fatal("end-group wire type accepted")
	}
}

// buildNestedStream builds:
//   Order { id=1 (varint), note=2 (string),
//           customer=3 (message { name=1 string, vip=2 varint,
//                                 address=3 message { city=1 string, zip=2 svarint } }),
//           tags=4 (packed varint), weights=5 (packed fixed32),
//           deltas=6 (packed fixed64), amounts=7 (repeated svarint) }
func buildNestedStream() []byte {
	addr := NewEncoder()
	addr.AddBytesField(1, []byte("Shanghai"))
	addr.AddZigZagField(2, -200000)

	cust := NewEncoder()
	cust.AddBytesField(1, []byte("alice"))
	cust.AddVarintField(2, 1)
	cust.AddBytesField(3, addr.Bytes())

	packedV := NewEncoder()
	for _, v := range []uint64{7, 8, 300} {
		packedV.AppendVarint(v)
	}
	packedF32 := NewEncoder()
	packedF32.AppendFixed32(11)
	packedF32.AppendFixed32(22)
	packedF64 := NewEncoder()
	packedF64.AppendFixed64(1 << 40)

	order := NewEncoder()
	order.AddVarintField(1, 42)
	order.AddBytesField(2, []byte("rush"))
	order.AddBytesField(3, cust.Bytes())
	order.AddBytesField(4, packedV.Bytes())
	order.AddBytesField(5, packedF32.Bytes())
	order.AddBytesField(6, packedF64.Bytes())
	order.AddZigZagField(7, -5)
	order.AddZigZagField(7, 9)
	return order.Bytes()
}

func orderSchema() *Schema {
	return &Schema{
		Name: "Order",
		Fields: []Field{
			{Name: "id", Number: 1, Kind: KindVarint, Required: true},
			{Name: "note", Number: 2, Kind: KindString},
			{Name: "customer", Number: 3, Kind: KindMessage, Nested: &Schema{
				Name: "Customer",
				Fields: []Field{
					{Name: "name", Number: 1, Kind: KindString, Required: true},
					{Name: "vip", Number: 2, Kind: KindVarint},
					{Name: "address", Number: 3, Kind: KindMessage, Nested: &Schema{
						Name: "Address",
						Fields: []Field{
							{Name: "city", Number: 1, Kind: KindString, Required: true},
							{Name: "zip", Number: 2, Kind: KindSVarint},
						},
					}},
				},
			}},
			{Name: "tags", Number: 4, Kind: KindPackedVarint},
			{Name: "weights", Number: 5, Kind: KindPackedFixed32},
			{Name: "deltas", Number: 6, Kind: KindPackedFixed64},
			{Name: "amounts", Number: 7, Kind: KindSVarint, Repeated: true},
		},
	}
}

func TestNestedMessageAndPackedDecode(t *testing.T) {
	msg, err := Parse(buildNestedStream())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := orderSchema().Decode(msg)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["id"] != uint64(42) || out["note"] != "rush" {
		t.Fatalf("scalars wrong: %v", out)
	}
	cust := out["customer"].(map[string]interface{})
	if cust["name"] != "alice" || cust["vip"] != uint64(1) {
		t.Fatalf("customer wrong: %v", cust)
	}
	addr := cust["address"].(map[string]interface{})
	if addr["city"] != "Shanghai" || addr["zip"] != int64(-200000) {
		t.Fatalf("address wrong: %v", addr)
	}
	if !reflect.DeepEqual(out["tags"], []uint64{7, 8, 300}) {
		t.Fatalf("packed varint wrong: %v", out["tags"])
	}
	if !reflect.DeepEqual(out["weights"], []uint32{11, 22}) {
		t.Fatalf("packed fixed32 wrong: %v", out["weights"])
	}
	if !reflect.DeepEqual(out["deltas"], []uint64{1 << 40}) {
		t.Fatalf("packed fixed64 wrong: %v", out["deltas"])
	}
	if !reflect.DeepEqual(out["amounts"], []interface{}{int64(-5), int64(9)}) {
		t.Fatalf("repeated svarint wrong: %v", out["amounts"])
	}
}

func TestSchemaValidationFailures(t *testing.T) {
	schema := orderSchema()

	// Missing required field "id".
	e := NewEncoder()
	e.AddBytesField(2, []byte("x"))
	msg, err := Parse(e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(msg); err == nil {
		t.Fatal("missing required field accepted")
	}

	// Wrong wire type: id sent as length-delimited.
	e = NewEncoder()
	e.AddBytesField(1, []byte("not-a-varint"))
	msg, err = Parse(e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(msg); err == nil {
		t.Fatal("wrong wire type accepted")
	}

	// Packed fixed32 payload not a multiple of 4.
	e = NewEncoder()
	e.AddVarintField(1, 1)
	e.AddBytesField(5, []byte{1, 2, 3})
	msg, err = Parse(e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(msg); err == nil {
		t.Fatal("misaligned packed fixed32 accepted")
	}

	// Nested message missing its own required field.
	badCust := NewEncoder() // no "name"
	badCust.AddVarintField(2, 1)
	e = NewEncoder()
	e.AddVarintField(1, 1)
	e.AddBytesField(3, badCust.Bytes())
	msg, err = Parse(e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(msg); err == nil {
		t.Fatal("nested required-field violation accepted")
	}
}

func TestUnknownFieldsPreserved(t *testing.T) {
	e := NewEncoder()
	e.AddVarintField(1, 7)
	e.AddVarintField(99, 1234) // unknown to schema
	msg, err := Parse(e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if msg[99][0].U64 != 1234 {
		t.Fatal("unknown field lost")
	}
	if err := orderSchema().Validate(msg); err != nil {
		t.Fatalf("unknown field should not fail validation: %v", err)
	}
}

package wire

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{0, 1, 127, 128, 300, 16384, 1<<21 - 1, 1 << 21,
		1<<32 - 1, 1 << 32, 1<<63 - 1, 1 << 63, math.MaxUint64}
	for _, v := range values {
		buf := AppendVarint(nil, v)
		if got := SizeVarint(v); got != len(buf) {
			t.Fatalf("SizeVarint(%d) = %d, encoded len = %d", v, got, len(buf))
		}
		r := NewReader(buf)
		got, err := r.ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("round trip: want %d, got %d", v, got)
		}
		if r.Remaining() != 0 {
			t.Fatalf("value %d: %d trailing bytes", v, r.Remaining())
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	full := AppendVarint(nil, 300) // 0xAC 0x02
	if len(full) != 2 {
		t.Fatalf("expected 2-byte encoding, got %d", len(full))
	}
	r := NewReader(full[:1])
	if _, err := r.ReadVarint(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if _, err := NewReader(nil).ReadVarint(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty buffer: want ErrTruncated, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10 continuation bytes: never terminates within the 64-bit budget.
	ten := make([]byte, 10)
	for i := range ten {
		ten[i] = 0x80
	}
	if _, err := NewReader(ten).ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("10 continuation bytes: want ErrOverflow, got %v", err)
	}
	// 10th byte carries more than one payload bit.
	over := append(AppendVarint(nil, 0), 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02)
	_ = over
	buf := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}
	if _, err := NewReader(buf).ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("10th byte > 1: want ErrOverflow, got %v", err)
	}
	// MaxUint64 is the largest legal value and must succeed.
	maxBuf := AppendVarint(nil, math.MaxUint64)
	if len(maxBuf) != 10 {
		t.Fatalf("MaxUint64 should encode to 10 bytes, got %d", len(maxBuf))
	}
	if v, err := NewReader(maxBuf).ReadVarint(); err != nil || v != math.MaxUint64 {
		t.Fatalf("MaxUint64: got %d, %v", v, err)
	}
}

func TestZigZag(t *testing.T) {
	cases64 := []int64{0, -1, 1, -2, 2, math.MinInt32, math.MaxInt32, math.MinInt64, math.MaxInt64}
	for _, v := range cases64 {
		if got := DecodeZigZag64(EncodeZigZag64(v)); got != v {
			t.Fatalf("zigzag64 round trip: want %d, got %d", v, got)
		}
	}
	cases32 := []int32{0, -1, 1, math.MinInt32, math.MaxInt32}
	for _, v := range cases32 {
		if got := DecodeZigZag32(EncodeZigZag32(v)); got != v {
			t.Fatalf("zigzag32 round trip: want %d, got %d", v, got)
		}
	}
	// Known mappings from the protobuf spec.
	if EncodeZigZag64(0) != 0 || EncodeZigZag64(-1) != 1 || EncodeZigZag64(1) != 2 || EncodeZigZag64(-2) != 3 {
		t.Fatal("zigzag mapping mismatch with spec")
	}
	// Wire-level read.
	r := NewReader(AppendZigZag64(nil, -12345))
	if v, err := r.ReadZigZag64(); err != nil || v != -12345 {
		t.Fatalf("ReadZigZag64: got %d, %v", v, err)
	}
}

func TestFixedRoundTrip(t *testing.T) {
	r := NewReader(AppendFixed32(nil, 0xDEADBEEF))
	if v, err := r.ReadFixed32(); err != nil || v != 0xDEADBEEF {
		t.Fatalf("fixed32: got %#x, %v", v, err)
	}
	r = NewReader(AppendFixed64(nil, 0x0102030405060708))
	if v, err := r.ReadFixed64(); err != nil || v != 0x0102030405060708 {
		t.Fatalf("fixed64: got %#x, %v", v, err)
	}
	// Truncation guards.
	for n := 0; n < 4; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed32(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed32 with %d bytes: want ErrTruncated, got %v", n, err)
		}
	}
	for n := 0; n < 8; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed64(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed64 with %d bytes: want ErrTruncated, got %v", n, err)
		}
	}
}

func TestReadTagValidation(t *testing.T) {
	// Field number 0 is illegal.
	if _, _, err := NewReader([]byte{0x00}).ReadTag(); !errors.Is(err, ErrInvalidFieldNumber) {
		t.Fatalf("field 0: want ErrInvalidFieldNumber, got %v", err)
	}
	// Wire types 3 and 4 (groups) are rejected.
	for _, tag := range []byte{0x0B, 0x0C} {
		if _, _, err := NewReader([]byte{tag}).ReadTag(); !errors.Is(err, ErrInvalidWireType) {
			t.Fatalf("tag %#x: want ErrInvalidWireType, got %v", tag, err)
		}
	}
	// A valid tag: field 15, wire type 2.
	num, wt, err := NewReader([]byte{15<<3 | 2}).ReadTag()
	if err != nil || num != 15 || wt != WireBytes {
		t.Fatalf("valid tag: got num=%d wt=%d err=%v", num, wt, err)
	}
}

// buildOrderBytes hand-encodes a 3-level nested message:
//
//	Order {
//	  1: uint64 id            (varint)
//	  2: Customer customer    (message)
//	  3: sint64 balance_delta (zigzag varint)
//	  4: repeated uint32 tags (packed)
//	  5: repeated uint64 weights (packed fixed64)
//	  6: fixed64 score
//	}
//	Customer { 1: string name; 2: Address address }
//	Address  { 1: string city; 2: fixed32 zip }
func buildOrderBytes() []byte {
	var addr []byte
	addr = AppendTag(addr, 1, WireBytes)
	addr = AppendString(addr, "Shanghai")
	addr = AppendTag(addr, 2, WireFixed32)
	addr = AppendFixed32(addr, 200001)

	var cust []byte
	cust = AppendTag(cust, 1, WireBytes)
	cust = AppendString(cust, "alice")
	cust = AppendTag(cust, 2, WireBytes)
	cust = AppendBytes(cust, addr)

	var order []byte
	order = AppendTag(order, 1, WireVarint)
	order = AppendVarint(order, 9876543210)
	order = AppendTag(order, 2, WireBytes)
	order = AppendBytes(order, cust)
	order = AppendTag(order, 3, WireVarint)
	order = AppendZigZag64(order, -4200)
	order = AppendTag(order, 4, WireBytes)
	order = AppendPackedVarints(order, 7, 8, 300, 1<<20)
	order = AppendTag(order, 5, WireBytes)
	order = AppendPackedFixed64(order, 11, 22, 33)
	order = AppendTag(order, 6, WireFixed64)
	order = AppendFixed64(order, 0xCAFEBABEDEADBEEF)
	return order
}

func TestNestedMessageDecode(t *testing.T) {
	msg, err := Decode(buildOrderBytes())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	id, ok := msg.One(1)
	if !ok || id.Type != WireVarint || id.U64 != 9876543210 {
		t.Fatalf("field 1: %+v ok=%v", id, ok)
	}

	delta, _ := msg.One(3)
	if got := delta.ZigZag64(); got != -4200 {
		t.Fatalf("field 3 zigzag: got %d", got)
	}

	custField, ok := msg.One(2)
	if !ok || custField.Type != WireBytes {
		t.Fatalf("field 2: %+v ok=%v", custField, ok)
	}
	cust, err := DecodeMessage(custField.Bytes)
	if err != nil {
		t.Fatalf("nested customer: %v", err)
	}
	name, _ := cust.One(1)
	if string(name.Bytes) != "alice" {
		t.Fatalf("customer name: %q", name.Bytes)
	}
	addrField, _ := cust.One(2)
	addr, err := DecodeMessage(addrField.Bytes)
	if err != nil {
		t.Fatalf("nested address: %v", err)
	}
	city, _ := addr.One(1)
	if string(city.Bytes) != "Shanghai" {
		t.Fatalf("city: %q", city.Bytes)
	}
	zip, _ := addr.One(2)
	if zip.Type != WireFixed32 || zip.F32 != 200001 {
		t.Fatalf("zip: %+v", zip)
	}

	tagsField, _ := msg.One(4)
	tags, err := DecodePackedVarints(tagsField.Bytes)
	if err != nil {
		t.Fatalf("packed tags: %v", err)
	}
	if want := []uint64{7, 8, 300, 1 << 20}; !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags: want %v, got %v", want, tags)
	}

	weightsField, _ := msg.One(5)
	weights, err := DecodePackedFixed64(weightsField.Bytes)
	if err != nil {
		t.Fatalf("packed weights: %v", err)
	}
	if want := []uint64{11, 22, 33}; !reflect.DeepEqual(weights, want) {
		t.Fatalf("weights: want %v, got %v", want, weights)
	}

	score, _ := msg.One(6)
	if score.Type != WireFixed64 || score.F64 != 0xCAFEBABEDEADBEEF {
		t.Fatalf("score: %+v", score)
	}
}

func TestPackedRepeated(t *testing.T) {
	want := []uint64{0, 1, 127, 128, math.MaxUint64}
	var body []byte
	for _, v := range want {
		body = AppendVarint(body, v)
	}
	vs, err := DecodePackedVarints(body)
	if err != nil {
		t.Fatalf("DecodePackedVarints: %v", err)
	}
	if !reflect.DeepEqual(vs, want) {
		t.Fatalf("want %v, got %v", want, vs)
	}
	var body32 []byte
	body32 = AppendFixed32(body32, 1)
	body32 = AppendFixed32(body32, 2)
	body32 = AppendFixed32(body32, 3)
	f32, err := DecodePackedFixed32(body32)
	if err != nil || !reflect.DeepEqual(f32, []uint32{1, 2, 3}) {
		t.Fatalf("packed fixed32: %v, %v", f32, err)
	}
	// Bad element alignment must be rejected, not silently dropped.
	if _, err := DecodePackedFixed32([]byte{1, 2, 3}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("misaligned fixed32: want ErrTruncated, got %v", err)
	}
	if _, err := DecodePackedFixed64([]byte{1, 2, 3, 4, 5}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("misaligned fixed64: want ErrTruncated, got %v", err)
	}
	// Truncated varint inside a packed body.
	if _, err := DecodePackedVarints([]byte{0xAC}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated packed varint: want ErrTruncated, got %v", err)
	}
}

// TestTruncationEveryOffset feeds every strict prefix of a valid nested
// buffer to Decode. The safety contract: Decode never panics, and any
// prefix that cuts through the trailing fixed64 field must fail with a
// bounds error instead of reading out of range.
func TestTruncationEveryOffset(t *testing.T) {
	full := buildOrderBytes()
	if _, err := Decode(full); err != nil {
		t.Fatalf("full buffer must decode: %v", err)
	}
	cutIntoLastField := len(full) - 8 // final field is tag + 8-byte fixed64
	for n := 0; n < len(full); n++ {
		prefix := full[:n]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix len %d panicked: %v", n, r)
				}
			}()
			_, err := Decode(prefix)
			if n >= cutIntoLastField && err == nil {
				t.Fatalf("prefix len %d of %d cuts the final fixed64 but decoded without error", n, len(full))
			}
			if n >= cutIntoLastField && !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrLengthOverflow) {
				t.Fatalf("prefix len %d: want bounds error, got %v", n, err)
			}
		}()
	}
}

func TestLengthOverflowGuard(t *testing.T) {
	// Field 1, wire type 2, length = 2^32, no payload.
	buf := AppendTag(nil, 1, WireBytes)
	buf = AppendVarint(buf, 1<<32)
	_, err := Decode(buf)
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("want ErrLengthOverflow, got %v", err)
	}
}

func TestMessageAccessors(t *testing.T) {
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 10)
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 20)
	msg, err := Decode(buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	occ := msg.Get(1)
	if len(occ) != 2 || occ[0].U64 != 10 || occ[1].U64 != 20 {
		t.Fatalf("Get(1): %+v", occ)
	}
	if got := msg.Get(99); len(got) != 0 {
		t.Fatalf("Get(99): %+v", got)
	}
	if _, ok := msg.One(99); ok {
		t.Fatal("One(99) should miss")
	}
}

func ExampleDecode() {
	// message P { 1: uint64 id = 150; 2: string name = "gopher" }
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 150)
	buf = AppendTag(buf, 2, WireBytes)
	buf = AppendString(buf, "gopher")

	msg, err := Decode(buf)
	if err != nil {
		panic(err)
	}
	id, _ := msg.One(1)
	name, _ := msg.One(2)
	fmt.Printf("id=%d name=%s\n", id.U64, string(name.Bytes))
	// Output:
	// id=150 name=gopher
}

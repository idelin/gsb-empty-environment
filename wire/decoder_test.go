package wire

import (
	"math"
	"reflect"
	"testing"
)

type Geo struct {
	Lat float64 `pb:"1,fixed64"`
	Lng float64 `pb:"2,fixed64"`
}

type Address struct {
	City string   `pb:"1,bytes"`
	Zip  uint32   `pb:"2,fixed32"`
	Geo  Geo      `pb:"3,bytes"` // nested by value, one more level
	Tags []string `pb:"4,bytes"` // repeated non-packed strings
}

type Person struct {
	ID      int64     `pb:"1,zigzag"`
	Name    string    `pb:"2,bytes"`
	Addr    *Address  `pb:"3,bytes"` // nested by pointer: 3 levels deep
	Scores  []int32   `pb:"4,zigzag,packed"`
	Vip     bool      `pb:"5,varint"`
	Stamp   uint64    `pb:"6,fixed64"`
	Raw     []byte    `pb:"7,bytes"`
	Weights []float32 `pb:"8,fixed32,packed"`
}

func encodePerson() []byte {
	var geo []byte
	geo = AppendTag(geo, 1, WireFixed64)
	geo = AppendFixed64(geo, math.Float64bits(31.2304))
	geo = AppendTag(geo, 2, WireFixed64)
	geo = AppendFixed64(geo, math.Float64bits(121.4737))

	var addr []byte
	addr = AppendTag(addr, 1, WireBytes)
	addr = AppendBytes(addr, []byte("Shanghai"))
	addr = AppendTag(addr, 2, WireFixed32)
	addr = AppendFixed32(addr, 200000)
	addr = AppendTag(addr, 3, WireBytes)
	addr = AppendBytes(addr, geo)
	addr = AppendTag(addr, 4, WireBytes)
	addr = AppendBytes(addr, []byte("home"))
	addr = AppendTag(addr, 4, WireBytes)
	addr = AppendBytes(addr, []byte("work"))

	var packed []byte
	for _, s := range []int32{-3, 0, 7, math.MinInt32, math.MaxInt32} {
		packed = AppendVarint(packed, EncodeZigZag32(s))
	}
	var packedF []byte
	for _, f := range []float32{1.5, -2.25, 3.75} {
		packedF = AppendFixed32(packedF, math.Float32bits(f))
	}

	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, EncodeZigZag64(-42))
	buf = AppendTag(buf, 2, WireBytes)
	buf = AppendBytes(buf, []byte("ada"))
	buf = AppendTag(buf, 3, WireBytes)
	buf = AppendBytes(buf, addr)
	buf = AppendTag(buf, 4, WireBytes)
	buf = AppendBytes(buf, packed)
	buf = AppendTag(buf, 5, WireVarint)
	buf = AppendVarint(buf, 1)
	buf = AppendTag(buf, 6, WireFixed64)
	buf = AppendFixed64(buf, 0xdeadbeefcafe)
	buf = AppendTag(buf, 7, WireBytes)
	buf = AppendBytes(buf, []byte{0x00, 0x01, 0xff})
	buf = AppendTag(buf, 8, WireBytes)
	buf = AppendBytes(buf, packedF)
	// Unknown field 99 must be skipped silently.
	buf = AppendTag(buf, 99, WireVarint)
	buf = AppendVarint(buf, 12345)
	return buf
}

func TestUnmarshalNestedAndPacked(t *testing.T) {
	var p Person
	if err := Unmarshal(encodePerson(), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.ID != -42 || p.Name != "ada" || !p.Vip || p.Stamp != 0xdeadbeefcafe {
		t.Fatalf("scalar mismatch: %+v", p)
	}
	if p.Addr == nil || p.Addr.City != "Shanghai" || p.Addr.Zip != 200000 {
		t.Fatalf("nested address mismatch: %+v", p.Addr)
	}
	if p.Addr.Geo.Lat != 31.2304 || p.Addr.Geo.Lng != 121.4737 {
		t.Fatalf("level-3 nested geo mismatch: %+v", p.Addr.Geo)
	}
	if !reflect.DeepEqual(p.Addr.Tags, []string{"home", "work"}) {
		t.Fatalf("repeated strings mismatch: %v", p.Addr.Tags)
	}
	wantScores := []int32{-3, 0, 7, math.MinInt32, math.MaxInt32}
	if !reflect.DeepEqual(p.Scores, wantScores) {
		t.Fatalf("packed zigzag scores: got %v, want %v", p.Scores, wantScores)
	}
	if !reflect.DeepEqual(p.Weights, []float32{1.5, -2.25, 3.75}) {
		t.Fatalf("packed fixed32 weights: got %v", p.Weights)
	}
	if !reflect.DeepEqual(p.Raw, []byte{0x00, 0x01, 0xff}) {
		t.Fatalf("raw bytes mismatch: %v", p.Raw)
	}
}

func TestUnmarshalNonPackedRepeatedAlsoAccepted(t *testing.T) {
	type Msg struct {
		Nums []int32 `pb:"1,zigzag,packed"`
	}
	var buf []byte
	for _, v := range []int32{5, -6} { // sent as individual varint fields
		buf = AppendTag(buf, 1, WireVarint)
		buf = AppendVarint(buf, EncodeZigZag32(v))
	}
	var m Msg
	if err := Unmarshal(buf, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(m.Nums, []int32{5, -6}) {
		t.Fatalf("got %v", m.Nums)
	}
}

func TestUnmarshalSingularLastWins(t *testing.T) {
	type Msg struct {
		V uint64 `pb:"1,varint"`
	}
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 1)
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 2)
	var m Msg
	if err := Unmarshal(buf, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.V != 2 {
		t.Fatalf("got %d, want 2 (last wins)", m.V)
	}
}

func TestUnmarshalTruncationSafetyEveryOffset(t *testing.T) {
	full := encodePerson()
	for cut := 0; cut < len(full); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic at cut %d: %v", cut, r)
				}
			}()
			var p Person
			// A cut landing exactly on a field boundary is a valid shorter
			// message; any error that does occur must be a truncation.
			err := Unmarshal(full[:cut], &p)
			if err != nil && !Is(err, ErrTruncated) {
				t.Fatalf("cut %d: got %v, want wrapped ErrTruncated", cut, err)
			}
		}()
	}
	// A guaranteed mid-field cut must fail with ErrTruncated.
	var p Person
	if err := Unmarshal(full[:len(full)-1], &p); !Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestUnmarshalWireTypeMismatch(t *testing.T) {
	type Msg struct {
		V uint64 `pb:"1,varint"`
	}
	var buf []byte
	buf = AppendTag(buf, 1, WireFixed32) // schema says varint
	buf = AppendFixed32(buf, 7)
	var m Msg
	if err := Unmarshal(buf, &m); !Is(err, ErrWireTypeMismatch) {
		t.Fatalf("got %v, want ErrWireTypeMismatch", err)
	}
}

func TestUnmarshalBadOut(t *testing.T) {
	if err := Unmarshal(nil, nil); err == nil {
		t.Fatal("expected error for nil out")
	}
	var notStruct int
	if err := Unmarshal(nil, &notStruct); err == nil {
		t.Fatal("expected error for non-struct out")
	}
}

func TestUnmarshalTruncatedPackedPayload(t *testing.T) {
	type Msg struct {
		Nums []int64 `pb:"1,varint,packed"`
	}
	packed := AppendVarint(nil, 1)
	packed = AppendVarint(packed, math.MaxUint64) // 10-byte varint
	packed = packed[:len(packed)-3]               // cut inside the varint
	var buf []byte
	buf = AppendTag(buf, 1, WireBytes)
	buf = AppendBytes(buf, packed)
	var m Msg
	if err := Unmarshal(buf, &m); !Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestUnmarshalNestedTruncationPropagates(t *testing.T) {
	type Inner struct {
		V uint64 `pb:"1,varint"`
	}
	type Outer struct {
		In Inner `pb:"1,bytes"`
	}
	var inner []byte
	inner = AppendTag(inner, 1, WireVarint)
	inner = AppendVarint(inner, 999)
	inner = inner[:len(inner)-1] // corrupt the nested payload
	var buf []byte
	buf = AppendTag(buf, 1, WireBytes)
	buf = AppendBytes(buf, inner)
	var o Outer
	if err := Unmarshal(buf, &o); !Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

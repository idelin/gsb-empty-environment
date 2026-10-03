package wire

import (
	"reflect"
	"testing"
)

// Three-level nested topology used across tests.
type Inner struct {
	Code  int64  `pb:"1,sint"`
	Label string `pb:"2,string"`
}

type Middle struct {
	ID     uint64   `pb:"1,varint"`
	Ratios []uint64 `pb:"2,fixed64,packed"`
	Deltas []int64  `pb:"3,sint,packed"`
	Inner  *Inner   `pb:"4,message"`
	Flags  []bool   `pb:"5,varint,repeated"`
}

type Outer struct {
	Name    string   `pb:"1,string"`
	Raw     []byte   `pb:"2,bytes"`
	Fixed32 uint32   `pb:"3,fixed32"`
	Fixed64 uint64   `pb:"4,fixed64"`
	Mids    []Middle `pb:"5,message,repeated"`
	Active  bool     `pb:"6,varint"`
	Score   float64  `pb:"7,fixed64"`
}

func sampleOuter() *Outer {
	return &Outer{
		Name:    "root",
		Raw:     []byte{0xde, 0xad, 0xbe, 0xef},
		Fixed32: 0xA5A5A5A5,
		Fixed64: 0x0123456789ABCDEF,
		Mids: []Middle{
			{
				ID:     42,
				Ratios: []uint64{1, 1 << 40, 1 << 63},
				Deltas: []int64{-5, 0, 7, -1000000},
				Inner:  &Inner{Code: -99, Label: "leaf"},
				Flags:  []bool{true, false, true},
			},
			{
				ID:     43,
				Deltas: []int64{1},
				Inner:  &Inner{Code: 0, Label: ""},
				Flags:  []bool{false},
			},
		},
		Active: true,
		Score:  -273.15,
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	want := sampleOuter()
	data, err := Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Outer
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("roundtrip mismatch:\nwant %+v\ngot  %+v", want, &got)
	}
}

func TestUnmarshalTruncationSafety(t *testing.T) {
	// A single-field message: every non-empty strict prefix cuts inside the
	// field and must produce a truncation-class error, never a panic.
	type One struct {
		Payload string `pb:"1,string"`
	}
	data, err := Marshal(&One{Payload: "0123456789abcdef"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 1; i < len(data); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d/%d panicked: %v", i, len(data), r)
				}
			}()
			var out One
			err := Unmarshal(data[:i], &out)
			if err == nil {
				t.Fatalf("prefix %d/%d: expected error, got success", i, len(data))
			}
			if te, ok := err.(*TruncatedError); !ok || !te.Is(ErrTruncated) {
				t.Fatalf("prefix %d: want *TruncatedError, got %T: %v", i, err, err)
			}
		}()
	}

	// Nested single-field message: truncating the inner payload at any
	// depth must surface as an error, not a panic or silent success.
	type Nest struct {
		Inner *One `pb:"1,message"`
	}
	ndata, err := Marshal(&Nest{Inner: &One{Payload: "deep-payload"}})
	if err != nil {
		t.Fatalf("marshal nested: %v", err)
	}
	for i := 1; i < len(ndata); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("nested prefix %d/%d panicked: %v", i, len(ndata), r)
				}
			}()
			var out Nest
			if err := Unmarshal(ndata[:i], &out); err == nil {
				t.Fatalf("nested prefix %d/%d: expected error, got success", i, len(ndata))
			}
		}()
	}

	// The full multi-field message: no prefix may panic, regardless of
	// whether it happens to land on a field boundary.
	big, err := Marshal(sampleOuter())
	if err != nil {
		t.Fatalf("marshal big: %v", err)
	}
	for i := 0; i < len(big); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("big prefix %d/%d panicked: %v", i, len(big), r)
				}
			}()
			var out Outer
			_ = Unmarshal(big[:i], &out)
		}()
	}
}

func TestUnmarshalSkipsUnknownFields(t *testing.T) {
	var buf []byte
	buf = EncodeTag(buf, 100, WireVarint)
	buf = EncodeVarint(buf, 12345)
	buf = EncodeTag(buf, 101, WireFixed64)
	buf = EncodeFixed64(buf, 0xdeadbeef)
	buf = EncodeTag(buf, 102, WireLengthDelimited)
	buf = EncodeVarint(buf, 4)
	buf = append(buf, "junk"...)
	buf = EncodeTag(buf, 103, WireFixed32)
	buf = EncodeFixed32(buf, 7)
	buf = EncodeTag(buf, 1, WireLengthDelimited)
	buf = EncodeVarint(buf, 2)
	buf = append(buf, "hi"...)

	var out struct {
		Name string `pb:"1,string"`
	}
	if err := Unmarshal(buf, &out); err != nil {
		t.Fatalf("unmarshal with unknowns: %v", err)
	}
	if out.Name != "hi" {
		t.Fatalf("Name=%q", out.Name)
	}
}

func TestUnmarshalWireTypeMismatch(t *testing.T) {
	var buf []byte
	buf = EncodeTag(buf, 1, WireFixed32) // schema says string (len-delimited)
	buf = EncodeFixed32(buf, 1)
	var out struct {
		Name string `pb:"1,string"`
	}
	err := Unmarshal(buf, &out)
	wte, ok := err.(*WireTypeError)
	if !ok {
		t.Fatalf("want *WireTypeError, got %T: %v", err, err)
	}
	if wte.FieldNum != 1 || wte.Want != WireLengthDelimited || wte.Got != WireFixed32 {
		t.Fatalf("WireTypeError: %+v", wte)
	}
}

func TestValidateSchemaErrors(t *testing.T) {
	// Duplicate field number.
	type Dup struct {
		A uint64 `pb:"1,varint"`
		B uint64 `pb:"1,varint"`
	}
	if err := Validate(Dup{}); err == nil {
		t.Fatalf("duplicate field number must fail validation")
	}
	// Kind incompatible with Go type.
	type Bad struct {
		A string `pb:"1,varint"`
	}
	if err := Validate(Bad{}); err == nil {
		t.Fatalf("string/varint mismatch must fail validation")
	}
	// Repeated on non-slice.
	type BadRep struct {
		A uint64 `pb:"1,varint,repeated"`
	}
	if err := Validate(BadRep{}); err == nil {
		t.Fatalf("repeated non-slice must fail validation")
	}
	// Packed on non-numeric kind.
	type BadPacked struct {
		A []string `pb:"1,string,packed"`
	}
	if err := Validate(BadPacked{}); err == nil {
		t.Fatalf("packed string must fail validation")
	}
	// Non-struct target.
	if err := Validate(42); err == nil {
		t.Fatalf("non-struct must fail validation")
	}
	// Unmarshal into non-pointer.
	var out struct{}
	if err := Unmarshal(nil, out); err == nil {
		t.Fatalf("non-pointer target must fail")
	}
}

func TestPackedRepeatedExactRestore(t *testing.T) {
	type Packed struct {
		Ints  []int64   `pb:"1,sint,packed"`
		U32   []uint32  `pb:"2,fixed32,packed"`
		Float []float64 `pb:"3,fixed64,packed"`
		Vars  []uint64  `pb:"4,varint,packed"`
	}
	want := Packed{
		Ints:  []int64{-1, 0, 1, -123456789, 987654321},
		U32:   []uint32{0, 1, 4294967295},
		Float: []float64{3.14, -2.71828, 0, 1e300},
		Vars:  []uint64{0, 127, 128, 1 << 63},
	}
	data, err := Marshal(&want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Packed
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("packed roundtrip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestParseMessageNested(t *testing.T) {
	// Hand-build: field 1 = varint 150; field 2 = nested {field 1 = "x"};
	// field 3 = nested {field 1 = nested {field 2 = fixed32 7}}.
	inner3 := EncodeTag(nil, 2, WireFixed32)
	inner3 = EncodeFixed32(inner3, 7)
	inner2 := EncodeTag(nil, 1, WireLengthDelimited)
	inner2 = EncodeVarint(inner2, uint64(len(inner3)))
	inner2 = append(inner2, inner3...)
	inner1 := EncodeTag(nil, 1, WireLengthDelimited)
	inner1 = EncodeVarint(inner1, 1)
	inner1 = append(inner1, 'x')

	var buf []byte
	buf = EncodeTag(buf, 1, WireVarint)
	buf = EncodeVarint(buf, 150)
	buf = EncodeTag(buf, 2, WireLengthDelimited)
	buf = EncodeVarint(buf, uint64(len(inner1)))
	buf = append(buf, inner1...)
	buf = EncodeTag(buf, 3, WireLengthDelimited)
	buf = EncodeVarint(buf, uint64(len(inner2)))
	buf = append(buf, inner2...)

	msg, err := ParseMessage(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(msg) != 3 || msg[0].Varint != 150 {
		t.Fatalf("top level: %+v", msg)
	}
	lvl2, err := ParseMessage(msg[1].Bytes)
	if err != nil || len(lvl2) != 1 || string(lvl2[0].Bytes) != "x" {
		t.Fatalf("level 2: %+v err=%v", lvl2, err)
	}
	lvl2b, err := ParseMessage(msg[2].Bytes)
	if err != nil || len(lvl2b) != 1 {
		t.Fatalf("level 2b: %+v err=%v", lvl2b, err)
	}
	lvl3, err := ParseMessage(lvl2b[0].Bytes)
	if err != nil || len(lvl3) != 1 || lvl3[0].Fixed32 != 7 {
		t.Fatalf("level 3: %+v err=%v", lvl3, err)
	}
}

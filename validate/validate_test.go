package validate

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"protowire/wire"
)

type inner struct {
	Label string `pb:"1,string" validate:"required"`
	Code  int64  `pb:"2,zigzag" validate:"min=-100,max=100"`
}

type sample struct {
	Name    string   `pb:"1,string" validate:"required"`
	ID      uint64   `pb:"2,varint" validate:"min=1,max=10000"`
	Balance int64    `pb:"3,zigzag"`
	Score   uint64   `pb:"4,fixed64"`
	Level   uint32   `pb:"5,fixed32"`
	Lucky   []int64  `pb:"6,packed"`
	Raw     []uint32 `pb:"7,packed"`
	In      *inner   `pb:"8,message"`
	Blob    []byte   `pb:"9,bytes"`
	Active  bool     `pb:"10,varint"`
}

func buildSample() []byte {
	var in []byte
	in = wire.AppendTag(in, 1, wire.WireLengthDelimited)
	in = wire.AppendBytes(in, []byte("nested"))
	in = wire.AppendTag(in, 2, wire.WireVarint)
	in = wire.AppendVarint(in, wire.EncodeZigZag(-42))

	var packed []byte
	for _, v := range []int64{7, -3, 42} {
		packed = wire.AppendVarint(packed, wire.EncodeZigZag(v))
	}
	var rawPacked []byte
	for _, v := range []uint32{100, 200} {
		rawPacked = wire.AppendFixed32(rawPacked, v)
	}

	var msg []byte
	msg = wire.AppendTag(msg, 1, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, []byte("Ada"))
	msg = wire.AppendTag(msg, 2, wire.WireVarint)
	msg = wire.AppendVarint(msg, 9001)
	msg = wire.AppendTag(msg, 3, wire.WireVarint)
	msg = wire.AppendVarint(msg, wire.EncodeZigZag(-123456))
	msg = wire.AppendTag(msg, 4, wire.WireFixed64)
	msg = wire.AppendFixed64(msg, 0xDEADBEEF)
	msg = wire.AppendTag(msg, 5, wire.WireFixed32)
	msg = wire.AppendFixed32(msg, 99)
	msg = wire.AppendTag(msg, 6, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, packed)
	msg = wire.AppendTag(msg, 7, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, rawPacked)
	msg = wire.AppendTag(msg, 8, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, in)
	msg = wire.AppendTag(msg, 9, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, []byte{1, 2, 3})
	msg = wire.AppendTag(msg, 10, wire.WireVarint)
	msg = wire.AppendVarint(msg, 1)
	return msg
}

func TestUnmarshalRestoresStruct(t *testing.T) {
	var s sample
	if err := Unmarshal(buildSample(), &s); err != nil {
		t.Fatal(err)
	}
	want := sample{
		Name: "Ada", ID: 9001, Balance: -123456,
		Score: 0xDEADBEEF, Level: 99,
		Lucky: []int64{7, -3, 42}, Raw: []uint32{100, 200},
		In:   &inner{Label: "nested", Code: -42},
		Blob: []byte{1, 2, 3}, Active: true,
	}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("got %+v\nwant %+v", s, want)
	}
}

func TestUnmarshalRequiresPointerToStruct(t *testing.T) {
	var s sample
	if err := Unmarshal(buildSample(), s); err == nil {
		t.Fatal("non-pointer must be rejected")
	}
	if err := Unmarshal(buildSample(), (*sample)(nil)); err == nil {
		t.Fatal("nil pointer must be rejected")
	}
}

func TestRequiredMissing(t *testing.T) {
	// Drop the Name field: rebuild a stream with only field 2.
	var msg []byte
	msg = wire.AppendTag(msg, 2, wire.WireVarint)
	msg = wire.AppendVarint(msg, 5)
	var s sample
	err := Unmarshal(msg, &s)
	var ve *Error
	if !errors.As(err, &ve) || ve.Rule != "required" || ve.Field != "Name" {
		t.Fatalf("err=%v, want required violation on Name", err)
	}
}

func TestRangeViolation(t *testing.T) {
	var msg []byte
	msg = wire.AppendTag(msg, 1, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, []byte("x"))
	msg = wire.AppendTag(msg, 2, wire.WireVarint)
	msg = wire.AppendVarint(msg, 99999) // exceeds max=10000
	var s sample
	err := Unmarshal(msg, &s)
	var ve *Error
	if !errors.As(err, &ve) || ve.Rule != "max" {
		t.Fatalf("err=%v, want max violation", err)
	}
}

func TestNestedRangeViolation(t *testing.T) {
	var in []byte
	in = wire.AppendTag(in, 1, wire.WireLengthDelimited)
	in = wire.AppendBytes(in, []byte("n"))
	in = wire.AppendTag(in, 2, wire.WireVarint)
	in = wire.AppendVarint(in, wire.EncodeZigZag(-500)) // below min=-100

	var msg []byte
	msg = wire.AppendTag(msg, 1, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, []byte("x"))
	msg = wire.AppendTag(msg, 2, wire.WireVarint)
	msg = wire.AppendVarint(msg, 1)
	msg = wire.AppendTag(msg, 8, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, in)

	var s sample
	err := Unmarshal(msg, &s)
	var ve *Error
	if !errors.As(err, &ve) || ve.Rule != "min" || !strings.Contains(ve.Detail, "-500") {
		t.Fatalf("err=%v, want nested min violation", err)
	}
}

func TestWireTypeMismatch(t *testing.T) {
	// Field 1 declared string but arrives as varint.
	var msg []byte
	msg = wire.AppendTag(msg, 1, wire.WireVarint)
	msg = wire.AppendVarint(msg, 7)
	var s sample
	err := Unmarshal(msg, &s)
	var ve *Error
	if !errors.As(err, &ve) || ve.Rule != "wiretype" {
		t.Fatalf("err=%v, want wiretype violation", err)
	}
}

func TestTruncatedInputPropagates(t *testing.T) {
	full := buildSample()
	var s sample
	err := Unmarshal(full[:len(full)-1], &s)
	if !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("err=%v, want wire.ErrTruncated", err)
	}
}

func TestOverflowGuard(t *testing.T) {
	type small struct {
		V uint32 `pb:"1,varint"`
	}
	var msg []byte
	msg = wire.AppendTag(msg, 1, wire.WireVarint)
	msg = wire.AppendVarint(msg, 1<<40) // does not fit uint32
	var s small
	err := Unmarshal(msg, &s)
	var ve *Error
	if !errors.As(err, &ve) || ve.Rule != "overflow" {
		t.Fatalf("err=%v, want overflow violation", err)
	}
}

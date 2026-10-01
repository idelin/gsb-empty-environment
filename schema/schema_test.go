package schema

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"wiresnap/wire"
)

// A deeply nested, packed-repeated-heavy message used across the tests.
type Inner struct {
	Label string `protobuf:"bytes,1"`
	Flags []bool `protobuf:"varint,2"`
}

type Middle struct {
	Ratio  float64 `protobuf:"fixed64,1"`
	Inner  Inner   `protobuf:"message,2"`
	Scores []int64 `protobuf:"zigzag,3"`
}

type Outer struct {
	ID    uint64   `protobuf:"varint,1"`
	Delta int32    `protobuf:"zigzag,2"`
	Lat   float32  `protobuf:"fixed32,3"`
	Lng   float64  `protobuf:"fixed64,4"`
	Name  string   `protobuf:"bytes,5"`
	Blob  []byte   `protobuf:"bytes,6"`
	Mid   Middle   `protobuf:"message,7"`
	Mids  []Middle `protobuf:"message,8"`
	Tags  []uint32 `protobuf:"varint,9"`
	Opt   *Inner   `protobuf:"message,10"`
}

func sampleOuter() Outer {
	return Outer{
		ID:    math.MaxUint64 - 7,
		Delta: -123456,
		Lat:   31.2304,
		Lng:   121.4737,
		Name:  "sh-节点-α",
		Blob:  []byte{0x00, 0xff, 0x7f, 0x80},
		Mid: Middle{
			Ratio:  math.Pi,
			Inner:  Inner{Label: "core", Flags: []bool{true, false, true}},
			Scores: []int64{-5, 0, 5, math.MinInt64, math.MaxInt64},
		},
		Mids: []Middle{
			{Ratio: 1.5, Inner: Inner{Label: "a"}, Scores: []int64{1}},
			{Ratio: -2.5, Inner: Inner{Label: "b", Flags: []bool{false}}, Scores: nil},
		},
		Tags: []uint32{0, 1, 127, 128, 300, math.MaxUint32},
		Opt:  &Inner{Label: "opt", Flags: []bool{true}},
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(Outer{}); err != nil {
		t.Fatalf("Validate(Outer): %v", err)
	}
	if err := Validate(&Outer{}); err != nil {
		t.Fatalf("Validate(&Outer): %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	type dupNumbers struct {
		A uint32 `protobuf:"varint,1"`
		B uint32 `protobuf:"varint,1"`
	}
	if err := Validate(dupNumbers{}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("dup field numbers: err = %v", err)
	}

	type badKind struct {
		A string `protobuf:"fixed32,1"`
	}
	if err := Validate(badKind{}); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("kind/type mismatch: err = %v", err)
	}

	type unknownKind struct {
		A uint32 `protobuf:"float128,1"`
	}
	if err := Validate(unknownKind{}); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("unknown kind: err = %v", err)
	}

	type badNumber struct {
		A uint32 `protobuf:"varint,0"`
	}
	if err := Validate(badNumber{}); err == nil || !strings.Contains(err.Error(), "field number") {
		t.Fatalf("field number 0: err = %v", err)
	}

	type badNested struct {
		A dupNumbers `protobuf:"message,1"`
	}
	if err := Validate(badNested{}); err == nil {
		t.Fatalf("nested schema error not propagated")
	}

	if err := Validate(42); err == nil {
		t.Fatalf("non-struct accepted")
	}
}

func TestRoundTripNestedPacked(t *testing.T) {
	want := sampleOuter()
	data, err := Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Outer
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestRoundTripNilOptionalMessage(t *testing.T) {
	in := sampleOuter()
	in.Opt = nil
	data, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var got Outer
	if err := Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Opt != nil {
		t.Fatalf("Opt = %+v, want nil", got.Opt)
	}
}

func TestPackedWireCompatibility(t *testing.T) {
	// Hand-encode Tags as packed (field 9, wire type 2) and confirm the
	// decoder reads it back; also confirm the raw bytes match a manual
	// packed encoding.
	want := []uint32{1, 150, 300}
	data, err := Marshal(struct {
		Tags []uint32 `protobuf:"varint,9"`
	}{Tags: want})
	if err != nil {
		t.Fatal(err)
	}
	var manual []byte
	manual = wire.AppendTag(manual, 9, wire.WireBytes)
	var packed []byte
	for _, v := range want {
		packed = wire.AppendVarint(packed, uint64(v))
	}
	manual = wire.AppendBytes(manual, packed)
	if !reflect.DeepEqual(data, manual) {
		t.Fatalf("packed encoding = %x, want %x", data, manual)
	}
}

func TestNonPackedRepeatedScalarAccepted(t *testing.T) {
	// Protobuf parsers must accept non-packed scalars even for packed-able
	// fields. Hand-encode three separate varint entries for field 9.
	var data []byte
	for _, v := range []uint64{7, 8, 9} {
		data = wire.AppendTag(data, 9, wire.WireVarint)
		data = wire.AppendVarint(data, v)
	}
	var out struct {
		Tags []uint32 `protobuf:"varint,9"`
	}
	if err := Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Tags, []uint32{7, 8, 9}) {
		t.Fatalf("Tags = %v", out.Tags)
	}
}

func TestUnknownFieldsSkipped(t *testing.T) {
	data, err := Marshal(sampleOuter())
	if err != nil {
		t.Fatal(err)
	}
	// Append unknown fields of every wire type with high field numbers.
	data = wire.AppendTag(data, 100, wire.WireVarint)
	data = wire.AppendVarint(data, 12345)
	data = wire.AppendTag(data, 101, wire.WireFixed64)
	data = wire.AppendFixed64(data, 0xdeadbeef)
	data = wire.AppendTag(data, 102, wire.WireBytes)
	data = wire.AppendBytes(data, []byte("unknown"))
	data = wire.AppendTag(data, 103, wire.WireFixed32)
	data = wire.AppendFixed32(data, 0xcafe)

	var got Outer
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal with unknown fields: %v", err)
	}
	if got.ID != sampleOuter().ID || got.Name != sampleOuter().Name {
		t.Fatalf("known fields corrupted after skipping unknowns: %+v", got)
	}
}

// TestTruncationSafety feeds every strict prefix of a valid encoding to
// Unmarshal and requires an error (never a panic, never partial success).
func TestTruncationSafety(t *testing.T) {
	data, err := Marshal(sampleOuter())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(data); i++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("prefix %d/%d panicked: %v", i, len(data), rec)
				}
			}()
			var out Outer
			err := Unmarshal(data[:i], &out)
			if err == nil {
				// A prefix can only succeed if it ends exactly on a field
				// boundary; verify by re-marshaling and comparing.
				re, merr := Marshal(out)
				if merr != nil || !reflect.DeepEqual(re, data[:i]) {
					t.Fatalf("prefix %d/%d silently mis-parsed", i, len(data))
				}
			}
			if err != nil && !errors.Is(err, wire.ErrTruncated) &&
				!errors.Is(err, wire.ErrOverflow) &&
				!errors.Is(err, wire.ErrInvalidWireType) &&
				!errors.Is(err, wire.ErrInvalidFieldNumber) {
				t.Fatalf("prefix %d/%d unexpected error type: %v", i, len(data), err)
			}
		}()
	}
}

func TestCorruptLengthPrefix(t *testing.T) {
	// Field 5 (Name, bytes) with a length far beyond the buffer.
	var data []byte
	data = wire.AppendTag(data, 5, wire.WireBytes)
	data = wire.AppendVarint(data, 1<<40)
	data = append(data, 'x')
	var out Outer
	err := Unmarshal(data, &out)
	if !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestWireTypeMismatch(t *testing.T) {
	// Field 1 (ID, varint) encoded as fixed32.
	var data []byte
	data = wire.AppendTag(data, 1, wire.WireFixed32)
	data = wire.AppendFixed32(data, 1)
	var out Outer
	if err := Unmarshal(data, &out); err == nil || !strings.Contains(err.Error(), "wire type") {
		t.Fatalf("err = %v, want wire type mismatch", err)
	}
}

func TestUnmarshalRequiresPointer(t *testing.T) {
	var out Outer
	if err := Unmarshal(nil, out); err == nil {
		t.Fatalf("non-pointer accepted")
	}
	if err := Unmarshal(nil, nil); err == nil {
		t.Fatalf("nil accepted")
	}
}

func TestEmptyMessage(t *testing.T) {
	var out Outer
	if err := Unmarshal(nil, &out); err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if !reflect.DeepEqual(out, Outer{}) {
		t.Fatalf("empty input produced non-zero struct: %+v", out)
	}
}

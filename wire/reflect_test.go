package wire

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Nested message: two levels of embedding plus packed repeated fields.
type Inner struct {
	Note   string  `protobuf:"bytes,1" validate:"required,minlen=1"`
	Weight float64 `protobuf:"fixed64,2"`
}

type Middle struct {
	Flag  bool    `protobuf:"varint,1"`
	Inner Inner   `protobuf:"bytes,2"`
	Tags  []int32 `protobuf:"zigzag,3,packed"`
}

type Outer struct {
	ID      uint64   `protobuf:"varint,1" validate:"min=1"`
	Name    string   `protobuf:"bytes,2" validate:"required,minlen=1,maxlen=32"`
	Balance int64    `protobuf:"zigzag,3" validate:"min=-1000000,max=1000000"`
	Raw     []byte   `protobuf:"bytes,4"`
	Mid     *Middle  `protobuf:"bytes,5"`
	Scores  []uint32 `protobuf:"varint,6,packed"`
	Rate    float32  `protobuf:"fixed32,7"`
	Count   int32    `protobuf:"varint,8"`
}

func encodeFixture() []byte {
	inner := NewBuffer()
	inner.AppendTag(1, LengthDelim)
	inner.AppendBytes([]byte("deep-note"))
	inner.AppendTag(2, Fixed64)
	inner.AppendFloat64(9.75)

	mid := NewBuffer()
	mid.AppendTag(1, Varint)
	mid.AppendVarint(1) // Flag = true
	mid.AppendTag(2, LengthDelim)
	mid.AppendBytes(inner.Bytes())
	// Packed sint32: -5, 0, 7
	packed := NewBuffer()
	for _, v := range []int64{-5, 0, 7} {
		packed.AppendZigZag(v)
	}
	mid.AppendTag(3, LengthDelim)
	mid.AppendBytes(packed.Bytes())

	out := NewBuffer()
	out.AppendTag(1, Varint)
	out.AppendVarint(9001)
	out.AppendTag(2, LengthDelim)
	out.AppendBytes([]byte("alice"))
	out.AppendTag(3, Varint)
	out.AppendZigZag(-123456)
	out.AppendTag(4, LengthDelim)
	out.AppendBytes([]byte{0xde, 0xad, 0xbe, 0xef})
	out.AppendTag(5, LengthDelim)
	out.AppendBytes(mid.Bytes())
	// Packed uint32 scores: 10, 20, 30, 40
	sc := NewBuffer()
	for _, v := range []uint64{10, 20, 30, 40} {
		sc.AppendVarint(v)
	}
	out.AppendTag(6, LengthDelim)
	out.AppendBytes(sc.Bytes())
	out.AppendTag(7, Fixed32)
	out.AppendFloat32(2.5)
	out.AppendTag(8, Varint)
	neg := int64(-7)
	out.AppendVarint(uint64(neg)) // negative int32 is sign-extended to a 64-bit varint
	return out.Bytes()
}

func TestUnmarshalNestedAndPacked(t *testing.T) {
	var o Outer
	if err := Unmarshal(encodeFixture(), &o); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if o.ID != 9001 || o.Name != "alice" || o.Balance != -123456 {
		t.Errorf("scalars: got %+v", o)
	}
	if !reflect.DeepEqual(o.Raw, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Errorf("raw bytes: got %x", o.Raw)
	}
	if o.Mid == nil || !o.Mid.Flag || o.Mid.Inner.Note != "deep-note" || o.Mid.Inner.Weight != 9.75 {
		t.Errorf("nested message: got %+v", o.Mid)
	}
	if !reflect.DeepEqual(o.Mid.Tags, []int32{-5, 0, 7}) {
		t.Errorf("packed sint32: got %v", o.Mid.Tags)
	}
	if !reflect.DeepEqual(o.Scores, []uint32{10, 20, 30, 40}) {
		t.Errorf("packed uint32: got %v", o.Scores)
	}
	if o.Rate != 2.5 {
		t.Errorf("float32: got %v", o.Rate)
	}
	if o.Count != -7 {
		t.Errorf("negative int32 varint: got %d", o.Count)
	}
	if err := Validate(&o); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestUnmarshalRepeatedUnpacked(t *testing.T) {
	// Same field number repeated as individual (unpacked) varints.
	b := NewBuffer()
	for _, v := range []uint64{3, 1, 4, 1, 5, 9, 2, 6} {
		b.AppendTag(6, Varint)
		b.AppendVarint(v)
	}
	var o Outer
	if err := Unmarshal(b.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	want := []uint32{3, 1, 4, 1, 5, 9, 2, 6}
	if !reflect.DeepEqual(o.Scores, want) {
		t.Errorf("unpacked repeated: got %v, want %v", o.Scores, want)
	}
}

func TestUnmarshalSkipsUnknownFields(t *testing.T) {
	data := encodeFixture()
	// Append an unknown field 99 (length-delimited) and field 100 (varint).
	b := NewBuffer()
	b.AppendTag(99, LengthDelim)
	b.AppendBytes([]byte("unknown"))
	b.AppendTag(100, Varint)
	b.AppendVarint(12345)
	data = append(data, b.Bytes()...)
	var o Outer
	if err := Unmarshal(data, &o); err != nil {
		t.Fatalf("unknown fields should be skipped: %v", err)
	}
	if o.ID != 9001 {
		t.Errorf("decode corrupted by unknown fields: %+v", o)
	}
}

// fieldBoundaries returns every offset in data that ends exactly on a
// top-level field boundary. A prefix cut at any other offset splits a field
// and must be rejected; a cut on a boundary is a valid (shorter) message.
func fieldBoundaries(t *testing.T, data []byte) map[int]bool {
	t.Helper()
	bounds := map[int]bool{0: true}
	r := NewReader(data)
	for !r.Done() {
		_, wt, err := r.ReadTag()
		if err != nil {
			t.Fatalf("fixture is not well-formed: %v", err)
		}
		if err := r.Skip(wt); err != nil {
			t.Fatalf("fixture is not well-formed: %v", err)
		}
		bounds[len(data)-r.Remaining()] = true
	}
	return bounds
}

// TestTruncationSafety cuts a valid stream at every possible position: the
// decoder must never panic, and must report an error exactly when the cut
// splits a field mid-way (out-of-bounds protection).
func TestTruncationSafety(t *testing.T) {
	full := encodeFixture()
	bounds := fieldBoundaries(t, full)
	midFieldCuts := 0
	for cut := 0; cut < len(full); cut++ {
		var o Outer
		err := Unmarshal(full[:cut], &o)
		if bounds[cut] {
			if err != nil {
				t.Errorf("cut at boundary %d/%d: unexpected error %v", cut, len(full), err)
			}
			continue
		}
		midFieldCuts++
		if err == nil {
			t.Errorf("cut at %d/%d splits a field but decoded without error", cut, len(full))
		}
	}
	if midFieldCuts == 0 {
		t.Fatal("fixture has no mid-field cut points; test is vacuous")
	}
	// The complete stream must succeed.
	var o Outer
	if err := Unmarshal(full, &o); err != nil {
		t.Errorf("full stream: %v", err)
	}
}

func TestTruncatedLengthPrefixInsideNested(t *testing.T) {
	// A nested message whose declared length overruns the parent boundary
	// must surface ErrTruncated, not a partial decode.
	type Holder struct {
		Payload []byte `protobuf:"bytes,1"`
	}
	b := NewBuffer()
	b.AppendTag(1, LengthDelim)
	b.AppendVarint(50) // declares 50 bytes
	b.AppendBytes([]byte("tiny"))
	raw := b.Bytes()[:2] // keep only tag + bogus length prefix
	var h Holder
	err := Unmarshal(raw, &h)
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("got %v, want ErrTruncated", err)
	}
}

func TestWireTypeMismatch(t *testing.T) {
	b := NewBuffer()
	b.AppendTag(1, Fixed32) // field 1 is declared varint
	b.AppendFixed32(1)
	var o Outer
	err := Unmarshal(b.Bytes(), &o)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected wire-type mismatch error, got %v", err)
	}
}

func TestUnmarshalBadTarget(t *testing.T) {
	var o Outer
	if err := Unmarshal(nil, o); err == nil {
		t.Error("non-pointer target should fail")
	}
	if err := Unmarshal(nil, nil); err == nil {
		t.Error("nil target should fail")
	}
}

func TestValidateViolations(t *testing.T) {
	o := Outer{ID: 0, Name: "", Balance: 2_000_000}
	err := Validate(&o)
	if err == nil {
		t.Fatal("expected validation errors")
	}
	ves, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	joined := err.Error()
	for _, want := range []string{"Outer.ID", "Outer.Name", "Outer.Balance"} {
		if !strings.Contains(joined, want) {
			t.Errorf("violation for %s missing in: %s", want, joined)
		}
	}
	if len(ves) < 3 {
		t.Errorf("expected >=3 violations, got %d", len(ves))
	}
}

func TestValidateNestedPath(t *testing.T) {
	o := Outer{ID: 5, Name: "ok", Mid: &Middle{Inner: Inner{Note: ""}}}
	err := Validate(&o)
	if err == nil || !strings.Contains(err.Error(), "Outer.Mid.Inner.Note") {
		t.Errorf("expected nested path violation, got %v", err)
	}
}

func TestMalformedTags(t *testing.T) {
	type Bad struct {
		X int32 `protobuf:"varint,zero"`
	}
	var b Bad
	if err := Unmarshal([]byte{}, &b); err == nil {
		t.Error("bad field number should be rejected")
	}
	type Dup struct {
		A int32 `protobuf:"varint,1"`
		B int32 `protobuf:"varint,1"`
	}
	var d Dup
	if err := Unmarshal([]byte{}, &d); err == nil {
		t.Error("duplicate field numbers should be rejected")
	}
}

func ExampleUnmarshal() {
	b := NewBuffer()
	b.AppendTag(1, Varint)
	b.AppendVarint(7)
	b.AppendTag(2, LengthDelim)
	b.AppendBytes([]byte("gopher"))
	var o Outer
	if err := Unmarshal(b.Bytes(), &o); err != nil {
		panic(err)
	}
	fmt.Println(o.ID, o.Name)
	// Output: 7 gopher
}

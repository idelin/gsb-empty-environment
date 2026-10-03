package wire

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

type Inner struct {
	ID   int32  `protobuf:"zigzag32,1" validate:"min=-100,max=100"`
	Name string `protobuf:"string,2" validate:"required,minlen=1,maxlen=32"`
}

type Middle struct {
	Flag  bool    `protobuf:"varint,1"`
	Inner Inner   `protobuf:"message,2"`
	Ratio float64 `protobuf:"fixed64,3"`
}

type Outer struct {
	UID     int64    `protobuf:"zigzag64,1" validate:"min=1"`
	Label   string   `protobuf:"string,2" validate:"required"`
	Scores  []int32  `protobuf:"zigzag32,3"`
	Mid     Middle   `protobuf:"message,4"`
	Tags    []Inner  `protobuf:"message,5"`
	CRC     uint32   `protobuf:"fixed32,6"`
	Raw     []byte   `protobuf:"bytes,7"`
	Samples []uint64 `protobuf:"varint,8"`
	Temp    float32  `protobuf:"fixed32,9"`
}

// encodeOuter hand-builds the wire stream for a fully populated Outer.
func encodeOuter() []byte {
	enc := NewEncoder()
	enc.AppendTag(1, WireVarint)
	enc.AppendZigZag64(42)
	enc.AppendTag(2, WireBytes)
	enc.AppendString("root")

	// field 3: packed repeated sint32
	packed := NewEncoder()
	for _, v := range []int32{-5, 0, 7, 300} {
		packed.AppendZigZag32(v)
	}
	enc.AppendTag(3, WireBytes)
	enc.AppendBytes(packed.Bytes())

	// field 4: nested Middle{Flag, Inner{ID, Name}, Ratio}
	inner := NewEncoder()
	inner.AppendTag(1, WireVarint)
	inner.AppendZigZag32(-7)
	inner.AppendTag(2, WireBytes)
	inner.AppendString("leaf")
	mid := NewEncoder()
	mid.AppendTag(1, WireVarint)
	mid.AppendVarint(1)
	mid.AppendTag(2, WireBytes)
	mid.AppendBytes(inner.Bytes())
	mid.AppendTag(3, WireFixed64)
	mid.AppendFixed64(math.Float64bits(3.5))
	enc.AppendTag(4, WireBytes)
	enc.AppendBytes(mid.Bytes())

	// field 5: repeated Inner messages
	for _, pair := range [][2]string{{"1", "a"}, {"-2", "b"}} {
		var id int32
		if pair[0] == "1" {
			id = 1
		} else {
			id = -2
		}
		tg := NewEncoder()
		tg.AppendTag(1, WireVarint)
		tg.AppendZigZag32(id)
		tg.AppendTag(2, WireBytes)
		tg.AppendString(pair[1])
		enc.AppendTag(5, WireBytes)
		enc.AppendBytes(tg.Bytes())
	}

	enc.AppendTag(6, WireFixed32)
	enc.AppendFixed32(0xcafebabe)
	enc.AppendTag(7, WireBytes)
	enc.AppendBytes([]byte{0xde, 0xad})

	// field 8: unpacked repeated varint (each with its own tag)
	for _, v := range []uint64{1, 300, math.MaxUint64} {
		enc.AppendTag(8, WireVarint)
		enc.AppendVarint(v)
	}

	enc.AppendTag(9, WireFixed32)
	enc.AppendFixed32(math.Float32bits(98.6))

	// field 100: unknown field, must be skipped
	enc.AppendTag(100, WireVarint)
	enc.AppendVarint(12345)

	return enc.Bytes()
}

func TestUnmarshalNestedAndPacked(t *testing.T) {
	var o Outer
	if err := Unmarshal(encodeOuter(), &o); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if o.UID != 42 {
		t.Fatalf("UID = %d, want 42", o.UID)
	}
	if o.Label != "root" {
		t.Fatalf("Label = %q", o.Label)
	}
	if !reflect.DeepEqual(o.Scores, []int32{-5, 0, 7, 300}) {
		t.Fatalf("Scores = %v", o.Scores)
	}
	if !o.Mid.Flag || o.Mid.Inner.ID != -7 || o.Mid.Inner.Name != "leaf" {
		t.Fatalf("Mid = %+v", o.Mid)
	}
	if o.Mid.Ratio != 3.5 {
		t.Fatalf("Ratio = %v", o.Mid.Ratio)
	}
	if len(o.Tags) != 2 || o.Tags[0].ID != 1 || o.Tags[0].Name != "a" || o.Tags[1].ID != -2 || o.Tags[1].Name != "b" {
		t.Fatalf("Tags = %+v", o.Tags)
	}
	if o.CRC != 0xcafebabe {
		t.Fatalf("CRC = %x", o.CRC)
	}
	if !reflect.DeepEqual(o.Raw, []byte{0xde, 0xad}) {
		t.Fatalf("Raw = %x", o.Raw)
	}
	if !reflect.DeepEqual(o.Samples, []uint64{1, 300, math.MaxUint64}) {
		t.Fatalf("Samples = %v", o.Samples)
	}
	if o.Temp != 98.6 {
		t.Fatalf("Temp = %v", o.Temp)
	}
	if err := Validate(&o); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestUnmarshalUnpackedRepeated(t *testing.T) {
	// Same field number repeated as individual varints.
	enc := NewEncoder()
	for _, v := range []int32{10, -20, 30} {
		enc.AppendTag(3, WireVarint)
		enc.AppendZigZag32(v)
	}
	var o Outer
	if err := Unmarshal(enc.Bytes(), &o); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(o.Scores, []int32{10, -20, 30}) {
		t.Fatalf("Scores = %v", o.Scores)
	}
}

func TestUnmarshalTruncatedField(t *testing.T) {
	full := encodeOuter()
	// Cut the stream at every possible point: each prefix must fail safely
	// (truncation error) or decode a strict prefix without ever panicking.
	truncErrs := 0
	for n := 0; n < len(full); n++ {
		var o Outer
		err := Unmarshal(full[:n], &o)
		if err == nil {
			continue // prefix ended exactly on a field boundary
		}
		if IsTruncated(err) || strings.Contains(err.Error(), "truncated") {
			truncErrs++
		}
	}
	if truncErrs == 0 {
		t.Fatal("no truncation errors observed across all prefixes")
	}
}

func TestUnmarshalTruncatedNestedLength(t *testing.T) {
	// A nested message whose length prefix overruns the outer buffer.
	enc := NewEncoder()
	enc.AppendTag(4, WireBytes)
	enc.AppendVarint(50) // claims 50 bytes
	enc.AppendRaw([]byte{0x08, 0x01})
	var o Outer
	err := Unmarshal(enc.Bytes(), &o)
	if !IsTruncated(err) {
		t.Fatalf("got %v, want truncation", err)
	}
}

func TestUnmarshalWireTypeMismatch(t *testing.T) {
	enc := NewEncoder()
	enc.AppendTag(1, WireFixed32) // UID expects varint
	enc.AppendFixed32(1)
	var o Outer
	err := Unmarshal(enc.Bytes(), &o)
	if err == nil || !strings.Contains(err.Error(), "wire type") {
		t.Fatalf("got %v, want wire type mismatch", err)
	}
}

func TestUnmarshalBadTarget(t *testing.T) {
	var notStruct int
	if err := Unmarshal([]byte{}, &notStruct); err == nil {
		t.Fatal("want error for non-struct target")
	}
	if err := Unmarshal([]byte{}, nil); err == nil {
		t.Fatal("want error for nil target")
	}
}

func TestUnmarshalDepthLimit(t *testing.T) {
	type node struct {
		Next *node `protobuf:"message,1"`
	}
	// Wrap an empty message in MaxDepth+1 nested message shells.
	payload := []byte{}
	for i := 0; i < MaxDepth+1; i++ {
		enc := NewEncoder()
		enc.AppendTag(1, WireBytes)
		enc.AppendBytes(payload)
		payload = enc.Bytes()
	}
	var root node
	err := Unmarshal(payload, &root)
	if err == nil {
		t.Fatal("want ErrMaxDepth, got nil")
	}
	if !strings.Contains(err.Error(), "maximum nesting depth") {
		t.Fatalf("got %v, want ErrMaxDepth", err)
	}
}

func TestValidateRules(t *testing.T) {
	valid := Outer{UID: 5, Label: "ok", Mid: Middle{Inner: Inner{ID: 3, Name: "x"}}}
	if err := Validate(&valid); err != nil {
		t.Fatalf("valid struct rejected: %v", err)
	}

	bad := Outer{
		UID:   0,                                       // violates min=1
		Label: "",                                      // violates required
		Mid:   Middle{Inner: Inner{ID: 500, Name: ""}}, // nested violations
	}
	err := Validate(&bad)
	if err == nil {
		t.Fatal("want validation errors")
	}
	ves, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("error type %T, want ValidationErrors", err)
	}
	if len(ves) != 5 {
		t.Fatalf("got %d violations (%v), want 5", len(ves), err)
	}
	joined := err.Error()
	for _, want := range []string{"UID", "Label", "ID", "Name"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("error %q missing field %s", joined, want)
		}
	}
}

func TestValidateMinMaxLen(t *testing.T) {
	type cfg struct {
		Ports []int  `validate:"minlen=1,maxlen=3"`
		Host  string `validate:"minlen=2"`
	}
	if err := Validate(&cfg{Ports: nil, Host: "x"}); err == nil {
		t.Fatal("want minlen violations")
	}
	if err := Validate(&cfg{Ports: []int{1, 2, 3, 4}, Host: "ab"}); err == nil {
		t.Fatal("want maxlen violation")
	}
	if err := Validate(&cfg{Ports: []int{1}, Host: "ab"}); err != nil {
		t.Fatalf("valid cfg rejected: %v", err)
	}
}

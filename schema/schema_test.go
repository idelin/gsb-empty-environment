package schema

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"wirefmt/wire"
)

// Deeply nested fixture: 3 levels, packed repeated, every scalar codec.
type Inner struct {
	Code  int64  `protobuf:"1,zigzag"`
	Label string `protobuf:"2,string"`
}

type Middle struct {
	Ratios  []float64 `protobuf:"1,packed,fixed64"`
	Inner   *Inner    `protobuf:"2,message"`
	Flags   []uint32  `protobuf:"3,packed,varint"`
	Payload []byte    `protobuf:"4,bytes"`
}

type Outer struct {
	ID     uint64    `protobuf:"1,varint"`
	Name   string    `protobuf:"2,string"`
	Score  int64     `protobuf:"3,zigzag"`
	Temp   float32   `protobuf:"4,fixed32"`
	Big    uint64    `protobuf:"5,fixed64"`
	Active bool      `protobuf:"6,varint"`
	Mids   []*Middle `protobuf:"7,message"`
	Deltas []int64   `protobuf:"8,packed,zigzag"`
}

func sampleOuter() *Outer {
	return &Outer{
		ID:     12345678901234,
		Name:   "nested-消息",
		Score:  -987654321,
		Temp:   36.6,
		Big:    0xdeadbeefcafebabe,
		Active: true,
		Mids: []*Middle{
			{
				Ratios:  []float64{0.5, -1.25, 3.14159},
				Inner:   &Inner{Code: -42, Label: "level-3"},
				Flags:   []uint32{1, 300, 65536},
				Payload: []byte{0xde, 0xad, 0xbe, 0xef},
			},
			{
				Ratios: []float64{2.5},
				Inner:  &Inner{Code: 7, Label: ""},
				Flags:  []uint32{0},
			},
		},
		Deltas: []int64{-1, 0, 1, -9223372036854775808, 9223372036854775807},
	}
}

func TestValidateGoodSchema(t *testing.T) {
	info, err := Validate(Outer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Fields) != 8 {
		t.Fatalf("got %d fields, want 8", len(info.Fields))
	}
	fi, ok := info.ByNumber(8)
	if !ok || !fi.Packed || fi.Codec != CodecZigZag {
		t.Errorf("field 8 = %+v ok=%v", fi, ok)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		v    interface{}
		want string
	}{
		{"non-struct", 42, "not a struct"},
		{"no tags", struct{ A int }{}, "no protobuf-tagged fields"},
		{"dup number", struct {
			A uint64 `protobuf:"1,varint"`
			B uint64 `protobuf:"1,varint"`
		}{}, "duplicate field number"},
		{"bad codec", struct {
			A uint64 `protobuf:"1,frobnicate"`
		}{}, "unknown codec"},
		{"codec/type mismatch", struct {
			A string `protobuf:"1,varint"`
		}{}, "incompatible"},
		{"packed non-slice", struct {
			A uint64 `protobuf:"1,packed,varint"`
		}{}, "packed requires a slice"},
		{"unpacked repeated scalar", struct {
			A []uint64 `protobuf:"1,varint"`
		}{}, "must be packed"},
		{"illegal number", struct {
			A uint64 `protobuf:"0,varint"`
		}{}, "illegal field number"},
		{"unexported", struct {
			a uint64 `protobuf:"1,varint"`
		}{}, "unexported"},
		{"bytes on non-slice", struct {
			A uint64 `protobuf:"1,bytes"`
		}{}, "requires []byte"},
		{"message on scalar", struct {
			A uint64 `protobuf:"1,message"`
		}{}, "requires struct"},
	}
	for _, c := range cases {
		_, err := Validate(c.v)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want substring %q", c.name, err, c.want)
		}
	}
}

func TestRoundTripDeepNesting(t *testing.T) {
	in := sampleOuter()
	data, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Outer
	if err := Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, &out) {
		t.Fatalf("round trip mismatch:\n in=%#v\nout=%#v", in, &out)
	}
}

func TestTruncationSafetyAtEveryByte(t *testing.T) {
	data, err := Marshal(sampleOuter())
	if err != nil {
		t.Fatal(err)
	}
	errCount := 0
	for i := 0; i < len(data); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d panicked: %v", i, r)
				}
			}()
			var out Outer
			// Prefixes ending at a field boundary are valid shorter
			// messages; prefixes cutting a field must error. Both are
			// safe: no panic, no out-of-bounds.
			if err := Unmarshal(data[:i], &out); err != nil {
				errCount++
			}
		}()
	}
	if errCount == 0 {
		t.Fatal("no prefix reported truncation; bounds guard not exercised")
	}
	t.Logf("%d/%d prefixes reported safe truncation errors", errCount, len(data))

	// A prefix cutting inside the first field (varint key+value of a
	// 13-digit ID) must always be an error.
	var out Outer
	if err := Unmarshal(data[:1], &out); err == nil {
		t.Fatal("1-byte prefix decoded without error")
	}
}

func TestUnknownFieldsSkipped(t *testing.T) {
	data, err := Marshal(&Inner{Code: 5, Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// Append unknown fields of every wire type.
	data = wire.AppendKey(data, 90, wire.WireVarint)
	data = wire.AppendUvarint(data, 777)
	data = wire.AppendKey(data, 91, wire.WireFixed32)
	data = wire.AppendFixed32(data, 1)
	data = wire.AppendKey(data, 92, wire.WireFixed64)
	data = wire.AppendFixed64(data, 2)
	data = wire.AppendKey(data, 93, wire.WireLengthPref)
	data = wire.AppendString(data, "ignored")

	var out Inner
	if err := Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Code != 5 || out.Label != "x" {
		t.Fatalf("got %+v", out)
	}
}

func TestWireTypeMismatchReported(t *testing.T) {
	// Field 1 of Outer is varint; send it as fixed32.
	var data []byte
	data = wire.AppendKey(data, 1, wire.WireFixed32)
	data = wire.AppendFixed32(data, 9)
	var out Outer
	err := Unmarshal(data, &out)
	if err == nil || !strings.Contains(err.Error(), "wire type mismatch") {
		t.Fatalf("err = %v, want wire type mismatch", err)
	}
}

func TestPackedTruncationInsideBody(t *testing.T) {
	// Field 8 of Outer: packed zigzag. Declare a length, then cut the body.
	var body []byte
	body = wire.AppendUvarint(body, uint64(wire.EncodeZigZag64(-5)))
	body = append(body, 0x80) // start of a second varint, never finished
	var data []byte
	data = wire.AppendKey(data, 8, wire.WireLengthPref)
	data = wire.AppendBytes(data, body)
	var out Outer
	if err := Unmarshal(data, &out); err == nil {
		t.Fatal("want error for truncated packed element")
	}
}

func TestUnmarshalNeedsPointer(t *testing.T) {
	if err := Unmarshal(nil, Outer{}); err == nil {
		t.Fatal("want error for non-pointer")
	}
}

func TestConcurrentCodec(t *testing.T) {
	in := sampleOuter()
	data, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, err := Validate(Outer{}); err != nil {
					t.Error(err)
					return
				}
				var out Outer
				if err := Unmarshal(data, &out); err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(in, &out) {
					t.Error("concurrent round trip mismatch")
					return
				}
				if _, err := Marshal(&out); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

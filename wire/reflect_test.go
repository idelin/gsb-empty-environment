package wire

import (
	"bytes"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type header struct {
	DeviceID uint64 `protobuf:"1,varint" validate:"required,min=1"`
	Timezone int32  `protobuf:"2,zigzag" validate:"min=-12,max=14"`
}

type sensorMeta struct {
	Firmware uint32 `protobuf:"1,fixed32"`
	Region   string `protobuf:"2,bytes" validate:"len=1..8"`
}

type reading struct {
	Header   *header     `protobuf:"1,message" validate:"required"`
	Label    string      `protobuf:"2,bytes"   validate:"required,len=1..64"`
	Samples  []int32     `protobuf:"3,zigzag,packed" validate:"len=1..1024"`
	Checksum uint32      `protobuf:"4,fixed32"`
	Serial   uint64      `protobuf:"5,fixed64"`
	Raw      []byte      `protobuf:"6,bytes"`
	Meta     sensorMeta  `protobuf:"7,message"`
	Flags    []uint32    `protobuf:"8,fixed32,packed"`
	Active   bool        `protobuf:"9,varint"`
	Notes    []string    // untagged: ignored
}

func sampleReading() reading {
	return reading{
		Header:   &header{DeviceID: 42, Timezone: 8},
		Label:    "engine-temp",
		Samples:  []int32{-3, 0, 7, 1024, -65536, 1<<30 - 1, -1 << 30},
		Checksum: 0xDEADBEEF,
		Serial:   0x0102030405060708,
		Raw:      []byte{0xCA, 0xFE, 0x00, 0xFF},
		Meta:     sensorMeta{Firmware: 0x00010002, Region: "cn-east"},
		Flags:    []uint32{1, 2, 0xFFFFFFFF},
		Active:   true,
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	in := sampleReading()
	data, err := Marshal(&in)
	if err != nil {
		t.Fatal(err)
	}
	var out reading
	if err := Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Header == nil || *out.Header != *in.Header {
		t.Errorf("header: got %+v want %+v", out.Header, in.Header)
	}
	if out.Label != in.Label || out.Checksum != in.Checksum || out.Serial != in.Serial || !out.Active {
		t.Errorf("scalars: got %+v", out)
	}
	if !reflect.DeepEqual(out.Samples, in.Samples) {
		t.Errorf("packed samples: got %v want %v", out.Samples, in.Samples)
	}
	if !reflect.DeepEqual(out.Flags, in.Flags) {
		t.Errorf("packed flags: got %v want %v", out.Flags, in.Flags)
	}
	if !bytes.Equal(out.Raw, in.Raw) {
		t.Errorf("raw bytes: got % x want % x", out.Raw, in.Raw)
	}
	if out.Meta != in.Meta {
		t.Errorf("nested meta: got %+v want %+v", out.Meta, in.Meta)
	}
}

func TestUnmarshalSkipsUnknownFields(t *testing.T) {
	// Hand-build a stream with fields the struct does not declare,
	// interleaved with known ones, covering every wire type.
	buf := NewBuffer()
	buf.AppendTag(50, WireVarint)
	buf.AppendVarint(999)
	buf.AppendTag(2, WireBytes) // known: Label
	buf.AppendString("x")
	buf.AppendTag(51, WireFixed64)
	buf.AppendFixed64(0xAA)
	buf.AppendTag(52, WireBytes)
	buf.AppendBytes([]byte("ignored"))
	buf.AppendTag(53, WireFixed32)
	buf.AppendFixed32(0xBB)

	var out reading
	if err := Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Label != "x" {
		t.Errorf("known field after unknown fields: got %q", out.Label)
	}
}

func TestUnmarshalTruncationNoPanic(t *testing.T) {
	in := sampleReading()
	data, err := Marshal(&in)
	if err != nil {
		t.Fatal(err)
	}
	sawErr := false
	for cut := 0; cut < len(data); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("cut=%d: panic %v", cut, r)
				}
			}()
			var out reading
			if err := Unmarshal(data[:cut], &out); err != nil {
				sawErr = true
			}
		}()
	}
	if !sawErr {
		t.Error("no truncation was ever detected")
	}
	// Cutting the final byte of the packed samples must be reported.
	var out reading
	if err := Unmarshal(data[:len(data)-1], &out); err == nil {
		t.Error("truncated tail accepted without error")
	}
}

func TestUnmarshalRejectsNonStruct(t *testing.T) {
	var i int
	if err := Unmarshal([]byte{0x08, 0x01}, &i); err != ErrNotStructPointer {
		t.Fatalf("got %v, want ErrNotStructPointer", err)
	}
	if err := Unmarshal([]byte{}, nil); err != ErrNotStructPointer {
		t.Fatalf("got %v, want ErrNotStructPointer", err)
	}
}

func TestUnmarshalWireTypeMismatch(t *testing.T) {
	// Field 2 (Label) declared bytes, but send a varint.
	buf := NewBuffer()
	buf.AppendTag(2, WireVarint)
	buf.AppendVarint(1)
	var out reading
	if err := Unmarshal(buf.Bytes(), &out); err == nil {
		t.Fatal("wire type mismatch accepted")
	}
}

func TestConcurrentUnmarshal(t *testing.T) {
	in := sampleReading()
	data, err := Marshal(&in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				var out reading
				if err := Unmarshal(data, &out); err != nil {
					t.Error(err)
					return
				}
				if out.Serial != in.Serial {
					t.Errorf("bad decode: %+v", out)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestValidateOK(t *testing.T) {
	in := sampleReading()
	if err := Validate(&in); err != nil {
		t.Fatalf("valid reading rejected: %v", err)
	}
}

func TestValidateViolations(t *testing.T) {
	in := sampleReading()
	in.Header.DeviceID = 0          // required + min=1
	in.Header.Timezone = 99         // max=14
	in.Label = ""                   // required + len
	in.Samples = nil                // len=1..1024
	in.Meta.Region = ""             // len=1..8
	err := Validate(&in)
	if err == nil {
		t.Fatal("violations not detected")
	}
	verrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("error type %T, want ValidationErrors", err)
	}
	if len(verrs) < 5 {
		t.Fatalf("got %d violations, want at least 5: %v", len(verrs), err)
	}
	joined := err.Error()
	for _, want := range []string{"DeviceID", "Timezone", "Label", "Samples", "Region"} {
		if !strings.Contains(joined, want) {
			t.Errorf("violation for %s missing in: %s", want, joined)
		}
	}
}

func TestValidateNilRoot(t *testing.T) {
	var r *reading
	if err := Validate(r); err == nil {
		t.Fatal("nil root accepted")
	}
}

func TestValidateNestedSliceElements(t *testing.T) {
	type batch struct {
		Items []header `protobuf:"1,message"`
	}
	b := batch{Items: []header{{DeviceID: 1, Timezone: 0}, {DeviceID: 0, Timezone: 0}}}
	err := Validate(&b)
	if err == nil || !strings.Contains(err.Error(), "Items[1].DeviceID") {
		t.Fatalf("nested slice element violation not reported: %v", err)
	}
}

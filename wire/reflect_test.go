package wire

import (
	"reflect"
	"strings"
	"testing"
)

// Deeply nested model: Envelope -> Header + []Sample (packed) + Meta -> []Tag.
type Tag struct {
	Key   string `protobuf:"bytes,1" validate:"nonzero"`
	Value string `protobuf:"bytes,2"`
}

type Meta struct {
	Tags   []Tag  `protobuf:"bytes,1"` // repeated embedded message
	Region string `protobuf:"bytes,2"`
}

type Header struct {
	ID      uint64 `protobuf:"varint,1" validate:"nonzero,min=1"`
	Version int32  `protobuf:"zigzag,2"`
	Flags   uint32 `protobuf:"fixed32,3"`
}

type Envelope struct {
	Header  Header  `protobuf:"bytes,1"`
	Samples []int32 `protobuf:"zigzag,2,packed"`
	Deltas  []int64 `protobuf:"varint,3"` // unpacked repeated
	Meta    *Meta   `protobuf:"bytes,4"`
	CRC     uint64  `protobuf:"fixed64,5"`
	Note    string  `protobuf:"bytes,6"`
}

func sampleEnvelope() *Envelope {
	return &Envelope{
		Header:  Header{ID: 42, Version: -3, Flags: 0xCAFEBABE},
		Samples: []int32{-150, 0, 1, 2, 300, -300},
		Deltas:  []int64{7, 8, 1 << 40},
		Meta: &Meta{
			Tags:   []Tag{{Key: "env", Value: "prod"}, {Key: "dc", Value: "cn-1"}},
			Region: "east",
		},
		CRC:  0x0102030405060708,
		Note: "nested & packed",
	}
}

func TestMarshalUnmarshalNestedPacked(t *testing.T) {
	want := sampleEnvelope()
	data, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Envelope
	if err := Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("round trip mismatch:\nwant %+v\ngot  %+v", want, &got)
	}
	if err := Validate(&got); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
}

// TestHandCraftedBytes verifies decoding of a byte stream written by hand
// (no encoder involved), proving the parser is a real wire-format reader.
func TestHandCraftedBytes(t *testing.T) {
	// Envelope{ Header{ID:1, Version:-1}, Samples:[1,-1,2] packed }
	var hdr []byte
	hdr = AppendVarint(hdr, Key(1, WireVarint))
	hdr = AppendVarint(hdr, 1)
	hdr = AppendVarint(hdr, Key(2, WireVarint))
	hdr = AppendVarint(hdr, ZigZagEncode(-1))

	var packed []byte
	for _, v := range []int64{1, -1, 2} {
		packed = AppendVarint(packed, ZigZagEncode(v))
	}

	var msg []byte
	msg = AppendVarint(msg, Key(1, WireBytes))
	msg = AppendVarint(msg, uint64(len(hdr)))
	msg = append(msg, hdr...)
	msg = AppendVarint(msg, Key(2, WireBytes))
	msg = AppendVarint(msg, uint64(len(packed)))
	msg = append(msg, packed...)

	var env Envelope
	if err := Unmarshal(msg, &env); err != nil {
		t.Fatal(err)
	}
	if env.Header.ID != 1 || env.Header.Version != -1 {
		t.Fatalf("header: %+v", env.Header)
	}
	if !reflect.DeepEqual(env.Samples, []int32{1, -1, 2}) {
		t.Fatalf("samples: %v", env.Samples)
	}
}

// fieldBoundaries walks a message with the low-level consumers and records
// every offset at which a whole field ends (clean cut points).
func fieldBoundaries(data []byte) map[int]bool {
	b := map[int]bool{0: true}
	off := 0
	for off < len(data) {
		key, n, err := ConsumeVarint(data[off:])
		if err != nil {
			break
		}
		off += n
		switch WireType(key & 0x7) {
		case WireVarint:
			_, n, err = ConsumeVarint(data[off:])
		case WireFixed64:
			_, n, err = ConsumeFixed64(data[off:])
		case WireBytes:
			var l uint64
			l, n, err = ConsumeVarint(data[off:])
			if err == nil {
				off += n
				n = int(l)
				if n > len(data)-off {
					err = ErrLengthOverflow
				}
			}
		case WireFixed32:
			_, n, err = ConsumeFixed32(data[off:])
		}
		if err != nil {
			break
		}
		off += n
		b[off] = true
	}
	return b
}

// TestUnmarshalTruncationSafety cuts a marshaled nested message at every
// byte boundary: never a panic; cuts strictly inside a field must error,
// cuts exactly on a field boundary decode a valid prefix.
func TestUnmarshalTruncationSafety(t *testing.T) {
	data, err := Marshal(sampleEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	boundaries := fieldBoundaries(data)
	for cut := 0; cut < len(data); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic at cut=%d: %v", cut, r)
				}
			}()
			var env Envelope
			err := Unmarshal(data[:cut], &env)
			if boundaries[cut] {
				if err != nil {
					t.Fatalf("cut=%d on field boundary should decode, got %v", cut, err)
				}
			} else if err == nil {
				t.Fatalf("cut=%d inside a field unexpectedly succeeded", cut)
			}
		}()
	}
}

// TestTruncatedPackedElement truncates inside the packed repeated body so
// the length prefix is intact but the last element's varint is cut.
func TestTruncatedPackedElement(t *testing.T) {
	var packed []byte
	packed = AppendVarint(packed, ZigZagEncode(5))
	packed = append(packed, 0x80) // dangling continuation byte

	var msg []byte
	msg = AppendVarint(msg, Key(2, WireBytes))
	msg = AppendVarint(msg, uint64(len(packed)))
	msg = append(msg, packed...)

	var env Envelope
	err := Unmarshal(msg, &env)
	if err == nil {
		t.Fatal("expected error for truncated packed element")
	}
	if !strings.Contains(err.Error(), "field 2") {
		t.Fatalf("error should name field 2, got: %v", err)
	}
}

func TestUnknownFieldsSkipped(t *testing.T) {
	var msg []byte
	msg = AppendVarint(msg, Key(99, WireVarint)) // unknown
	msg = AppendVarint(msg, 1234)
	msg = AppendVarint(msg, Key(6, WireBytes)) // Envelope.Note
	msg = AppendVarint(msg, 2)
	msg = append(msg, "hi"...)
	msg = AppendVarint(msg, Key(100, WireFixed64))
	msg = AppendFixed64(msg, 9)

	var env Envelope
	if err := Unmarshal(msg, &env); err != nil {
		t.Fatal(err)
	}
	if env.Note != "hi" {
		t.Fatalf("note=%q", env.Note)
	}
}

func TestValidateRules(t *testing.T) {
	bad := sampleEnvelope()
	bad.Header.ID = 0
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "Header.ID") {
		t.Fatalf("expected Header.ID violation, got %v", err)
	}
	bad = sampleEnvelope()
	bad.Meta.Tags[1].Key = ""
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "Tags[1].Key") {
		t.Fatalf("expected Tags[1].Key violation, got %v", err)
	}
}

func TestWireTypeMismatch(t *testing.T) {
	// Field 5 (CRC) declared fixed64 in the struct, sent as varint.
	var msg []byte
	msg = AppendVarint(msg, Key(5, WireVarint))
	msg = AppendVarint(msg, 1)
	var env Envelope
	if err := Unmarshal(msg, &env); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected wire type mismatch, got %v", err)
	}
}

package wire

import (
	"strings"
	"testing"
)

// Schema used across the reflection tests: three nesting levels, packed
// and unpacked repeated fields, zigzag, fixed widths and required fields.
type Inner struct {
	Code int64 `wire:"zigzag64,1,required"`
}

type Middle struct {
	Name   string   `wire:"string,1,required"`
	Inner  Inner    `wire:"message,2"`
	Scores []uint64 `wire:"varint,3,packed"`
	Tags   []string `wire:"string,4"`
}

type Envelope struct {
	ID      uint64  `wire:"varint,1,required"`
	Delta   int64   `wire:"zigzag64,2"`
	Flag    bool    `wire:"varint,3"`
	Ratio32 uint32  `wire:"fixed32,4"`
	Big     uint64  `wire:"fixed64,5"`
	Body    *Middle `wire:"message,6,required"`
	Raw     []byte  `wire:"bytes,7"`
	Deltas  []int32 `wire:"zigzag32,8,packed"`
}

func buildEnvelope() []byte {
	inner := AppendTag(nil, 1, WireVarint)
	inner = AppendVarint(inner, EncodeZigZag64(-5))

	mid := AppendTag(nil, 1, WireBytes)
	mid = AppendBytes(mid, []byte("core"))
	mid = AppendTag(mid, 2, WireBytes)
	mid = AppendBytes(mid, inner)
	var packed []byte
	for _, v := range []uint64{10, 20, 300} {
		packed = AppendVarint(packed, v)
	}
	mid = AppendTag(mid, 3, WireBytes)
	mid = AppendBytes(mid, packed)
	mid = AppendTag(mid, 4, WireBytes)
	mid = AppendBytes(mid, []byte("x"))
	mid = AppendTag(mid, 4, WireBytes)
	mid = AppendBytes(mid, []byte("yy"))

	var packedZ []byte
	for _, v := range []int32{-1, 0, 7} {
		packedZ = AppendVarint(packedZ, uint64(EncodeZigZag32(v)))
	}

	env := AppendTag(nil, 1, WireVarint)
	env = AppendVarint(env, 9001)
	env = AppendTag(env, 2, WireVarint)
	env = AppendVarint(env, EncodeZigZag64(-12345))
	env = AppendTag(env, 3, WireVarint)
	env = AppendVarint(env, 1)
	env = AppendTag(env, 4, WireFixed32)
	env = AppendFixed32(env, 0xdeadbeef)
	env = AppendTag(env, 5, WireFixed64)
	env = AppendFixed64(env, 0x0102030405060708)
	env = AppendTag(env, 6, WireBytes)
	env = AppendBytes(env, mid)
	env = AppendTag(env, 7, WireBytes)
	env = AppendBytes(env, []byte{0xca, 0xfe})
	env = AppendTag(env, 8, WireBytes)
	env = AppendBytes(env, packedZ)
	return env
}

func TestUnmarshalNestedPacked(t *testing.T) {
	var env Envelope
	if err := Unmarshal(buildEnvelope(), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.ID != 9001 {
		t.Errorf("ID = %d, want 9001", env.ID)
	}
	if env.Delta != -12345 {
		t.Errorf("Delta = %d, want -12345", env.Delta)
	}
	if !env.Flag {
		t.Errorf("Flag = false, want true")
	}
	if env.Ratio32 != 0xdeadbeef {
		t.Errorf("Ratio32 = %#x", env.Ratio32)
	}
	if env.Big != 0x0102030405060708 {
		t.Errorf("Big = %#x", env.Big)
	}
	if env.Body == nil {
		t.Fatal("Body is nil")
	}
	if env.Body.Name != "core" {
		t.Errorf("Body.Name = %q", env.Body.Name)
	}
	if env.Body.Inner.Code != -5 {
		t.Errorf("Body.Inner.Code = %d, want -5", env.Body.Inner.Code)
	}
	if len(env.Body.Scores) != 3 || env.Body.Scores[0] != 10 || env.Body.Scores[1] != 20 || env.Body.Scores[2] != 300 {
		t.Errorf("Body.Scores = %v", env.Body.Scores)
	}
	if len(env.Body.Tags) != 2 || env.Body.Tags[0] != "x" || env.Body.Tags[1] != "yy" {
		t.Errorf("Body.Tags = %v", env.Body.Tags)
	}
	if len(env.Raw) != 2 || env.Raw[0] != 0xca || env.Raw[1] != 0xfe {
		t.Errorf("Raw = % x", env.Raw)
	}
	wantD := []int32{-1, 0, 7}
	if len(env.Deltas) != len(wantD) {
		t.Fatalf("Deltas = %v", env.Deltas)
	}
	for i := range wantD {
		if env.Deltas[i] != wantD[i] {
			t.Fatalf("Deltas = %v, want %v", env.Deltas, wantD)
		}
	}
}

func TestUnmarshalMissingRequired(t *testing.T) {
	// Drop the required Body field (6): only ID present.
	msg := AppendTag(nil, 1, WireVarint)
	msg = AppendVarint(msg, 1)
	var env Envelope
	err := Unmarshal(msg, &env)
	if err == nil {
		t.Fatal("missing required field accepted")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	found := false
	for _, p := range ve.Problems {
		if p.Num == 6 && strings.Contains(p.Path, "Body") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want missing Body", ve.Problems)
	}
}

func TestUnmarshalWireTypeMismatch(t *testing.T) {
	// ID is declared varint but arrives as fixed32.
	msg := AppendTag(nil, 1, WireFixed32)
	msg = AppendFixed32(msg, 7)
	mid := AppendTag(nil, 1, WireBytes)
	mid = AppendBytes(mid, []byte("m"))
	msg = AppendTag(msg, 6, WireBytes)
	msg = AppendBytes(msg, mid)
	var env Envelope
	err := Unmarshal(msg, &env)
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error = %v (%T), want *ValidationError", err, err)
	}
	found := false
	for _, p := range ve.Problems {
		if p.Num == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want type mismatch on field 1", ve.Problems)
	}
}

func TestUnmarshalUnknownFieldsSkipped(t *testing.T) {
	msg := buildEnvelope()
	// Append unknown fields of every wire type; decoding must still succeed.
	msg = AppendTag(msg, 100, WireVarint)
	msg = AppendVarint(msg, 1)
	msg = AppendTag(msg, 101, WireFixed64)
	msg = AppendFixed64(msg, 2)
	msg = AppendTag(msg, 102, WireBytes)
	msg = AppendBytes(msg, []byte("ignored"))
	msg = AppendTag(msg, 103, WireFixed32)
	msg = AppendFixed32(msg, 3)
	var env Envelope
	if err := Unmarshal(msg, &env); err != nil {
		t.Fatalf("unknown fields not skipped: %v", err)
	}
	if env.ID != 9001 {
		t.Errorf("ID = %d after unknown fields", env.ID)
	}
}

func TestUnmarshalTruncationSweep(t *testing.T) {
	full := buildEnvelope()
	// Top-level field boundaries: truncating exactly there yields a valid
	// shorter message; any other cut must be reported as a typed error.
	boundaries := map[int]bool{0: true}
	rest, off := full, 0
	for len(rest) > 0 {
		_, n, err := NextField(rest)
		if err != nil {
			t.Fatalf("boundary scan: %v", err)
		}
		off += n
		boundaries[off] = true
		rest = rest[n:]
	}
	for i := 0; i <= len(full); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic at truncation %d: %v", i, r)
				}
			}()
			var env Envelope
			err := Unmarshal(full[:i], &env)
			if boundaries[i] {
				return // valid prefix; may or may not satisfy required fields
			}
			if err == nil {
				t.Fatalf("mid-field truncation to %d bytes accepted", i)
			}
			// Must be a typed error, never a bare panic or nil.
			switch err.(type) {
			case *ParseError, *ValidationError:
			default:
				t.Fatalf("truncation %d: unexpected error type %T", i, err)
			}
		}()
	}
}

func TestUnmarshalBadTarget(t *testing.T) {
	var notStruct int
	if err := Unmarshal(nil, &notStruct); err == nil {
		t.Error("non-struct target accepted")
	}
	if err := Unmarshal(nil, nil); err == nil {
		t.Error("nil target accepted")
	}
}

func TestUnmarshalNestedTruncationReported(t *testing.T) {
	// A nested message whose payload is cut short must surface as a
	// validation problem on the parent field, not a panic.
	inner := AppendTag(nil, 1, WireVarint)
	inner = AppendVarint(inner, EncodeZigZag64(3))
	// Corrupt: declare a length one byte longer than the payload.
	mid := AppendTag(nil, 2, WireBytes)
	mid = AppendVarint(mid, uint64(len(inner)+1))
	mid = append(mid, inner...)
	env := AppendTag(nil, 1, WireVarint)
	env = AppendVarint(env, 1)
	env = AppendTag(env, 6, WireBytes)
	env = AppendBytes(env, mid)
	var out Envelope
	if err := Unmarshal(env, &out); err == nil {
		t.Fatal("corrupt nested message accepted")
	}
}

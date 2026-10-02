package wire

import (
	"errors"
	"reflect"
	"testing"
)

// buildNested crafts: outer{ 1: varint 7, 2: inner{ 1: "hi", 2: core{ 1: fixed64 9 } }, 3: packed[1,2,300] }
func buildNested() []byte {
	var core []byte
	core = AppendTag(core, 1, WireFixed64)
	core = AppendFixed64(core, 9)

	var inner []byte
	inner = AppendTag(inner, 1, WireLengthDelimited)
	inner = AppendBytes(inner, []byte("hi"))
	inner = AppendTag(inner, 2, WireLengthDelimited)
	inner = AppendBytes(inner, core)

	var packed []byte
	for _, v := range []uint64{1, 2, 300} {
		packed = AppendVarint(packed, v)
	}

	var outer []byte
	outer = AppendTag(outer, 1, WireVarint)
	outer = AppendVarint(outer, 7)
	outer = AppendTag(outer, 2, WireLengthDelimited)
	outer = AppendBytes(outer, inner)
	outer = AppendTag(outer, 3, WireLengthDelimited)
	outer = AppendBytes(outer, packed)
	return outer
}

func TestParseNestedMessage(t *testing.T) {
	fields, err := ParseMessage(buildNested())
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("want 3 outer fields, got %d", len(fields))
	}
	if fields[0].Number != 1 || fields[0].Varint != 7 {
		t.Fatalf("field 1: %+v", fields[0])
	}

	inner, err := fields[1].Nested()
	if err != nil {
		t.Fatal(err)
	}
	if len(inner) != 2 || string(inner[0].Bytes) != "hi" {
		t.Fatalf("inner: %+v", inner)
	}

	core, err := inner[1].Nested()
	if err != nil {
		t.Fatal(err)
	}
	if len(core) != 1 || core[0].Fixed64 != 9 {
		t.Fatalf("core: %+v", core)
	}
}

func TestParsePackedRepeated(t *testing.T) {
	fields, err := ParseMessage(buildNested())
	if err != nil {
		t.Fatal(err)
	}
	vals, err := DecodePackedVarints(fields[2].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vals, []uint64{1, 2, 300}) {
		t.Fatalf("packed varints: %v", vals)
	}
}

func TestParsePackedFixed(t *testing.T) {
	var p32 []byte
	for _, v := range []uint32{5, 6} {
		p32 = AppendFixed32(p32, v)
	}
	got32, err := DecodePackedFixed32(p32)
	if err != nil || !reflect.DeepEqual(got32, []uint32{5, 6}) {
		t.Fatalf("packed fixed32: %v err=%v", got32, err)
	}

	var p64 []byte
	p64 = AppendFixed64(p64, 0xAABB)
	got64, err := DecodePackedFixed64(p64)
	if err != nil || !reflect.DeepEqual(got64, []uint64{0xAABB}) {
		t.Fatalf("packed fixed64: %v err=%v", got64, err)
	}

	if _, err := DecodePackedFixed32([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidPackedLength) {
		t.Fatalf("fixed32 len%%4: err=%v", err)
	}
	if _, err := DecodePackedFixed64([]byte{1, 2, 3, 4}); !errors.Is(err, ErrInvalidPackedLength) {
		t.Fatalf("fixed64 len%%8: err=%v", err)
	}
}

func TestParsePackedZigZag(t *testing.T) {
	var p []byte
	for _, v := range []int64{-1, 0, 1, -300} {
		p = AppendVarint(p, EncodeZigZag(v))
	}
	got, err := DecodePackedZigZags(p)
	if err != nil || !reflect.DeepEqual(got, []int64{-1, 0, 1, -300}) {
		t.Fatalf("packed zigzag: %v err=%v", got, err)
	}
}

func TestTruncationSafetyGuards(t *testing.T) {
	full := buildNested()
	// Every strict prefix must either parse (it happens to end exactly on
	// a field boundary) or fail with a sentinel error - never panic nor
	// read out of bounds. Prefixes cutting inside a field must fail.
	failures := 0
	for i := 0; i < len(full); i++ {
		_, err := ParseMessage(full[:i])
		if err == nil {
			continue
		}
		failures++
		if !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrOverflow) &&
			!errors.Is(err, ErrInvalidWireType) && !errors.Is(err, ErrInvalidFieldNumber) {
			t.Fatalf("prefix %d: unexpected error %v", i, err)
		}
	}
	if failures == 0 {
		t.Fatal("expected at least one truncated prefix to fail")
	}
	// Cutting inside the nested length-delimited payload must fail.
	inner := AppendTag(nil, 1, WireLengthDelimited)
	inner = AppendBytes(inner, []byte("hello world"))
	if _, err := ParseMessage(inner[:len(inner)-3]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("mid-payload cut: err=%v, want ErrTruncated", err)
	}
	// A tag claiming a huge length-delimited payload.
	evil := AppendTag(nil, 1, WireLengthDelimited)
	evil = AppendVarint(evil, 1<<40)
	if _, err := ParseMessage(evil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("huge length: err=%v, want ErrTruncated", err)
	}
	// Truncated fixed64 mid-message.
	trunc := AppendTag(nil, 2, WireFixed64)
	trunc = append(trunc, 0x01, 0x02)
	if _, err := ParseMessage(trunc); !errors.Is(err, ErrTruncated) {
		t.Fatalf("fixed64 trunc: err=%v", err)
	}
	// Reserved wire type on the wire.
	bad := AppendVarint(nil, 1<<3|6)
	if _, err := ParseMessage(bad); !errors.Is(err, ErrInvalidWireType) {
		t.Fatalf("reserved wiretype: err=%v", err)
	}
}

func TestByNumber(t *testing.T) {
	var msg []byte
	for _, v := range []uint64{10, 20, 30} {
		msg = AppendTag(msg, 1, WireVarint)
		msg = AppendVarint(msg, v)
	}
	fields, err := ParseMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	ones := ByNumber(fields, 1)
	if len(ones) != 3 || ones[2].Varint != 30 {
		t.Fatalf("ByNumber: %+v", ones)
	}
	if got := ByNumber(fields, 99); len(got) != 0 {
		t.Fatalf("missing number should yield no fields, got %+v", got)
	}
}

package wireadmit

import (
	"bytes"
	"errors"
	"testing"
)

// test schema:
//
//	1 singular Varint
//	2 singular Fixed64
//	3 singular Bytes
//	4 repeated Varint (packable)
//	5 repeated Fixed32 (packable)
//	6 singular Message -> nested: 1 repeated Varint
//	7 singular Group   -> nested: 1 singular Varint
//	8 repeated Bytes (not packable)
func testSchema(t *testing.T) *Schema {
	t.Helper()
	nested, err := NewSchema([]Rule{
		{Number: 1, Type: Varint, Cardinality: Repeated},
	})
	if err != nil {
		t.Fatalf("nested schema: %v", err)
	}
	groupNested, err := NewSchema([]Rule{
		{Number: 1, Type: Varint, Cardinality: Singular},
	})
	if err != nil {
		t.Fatalf("group schema: %v", err)
	}
	schema, err := NewSchema([]Rule{
		{Number: 1, Type: Varint, Cardinality: Singular},
		{Number: 2, Type: Fixed64, Cardinality: Singular},
		{Number: 3, Type: Bytes, Cardinality: Singular},
		{Number: 4, Type: Varint, Cardinality: Repeated},
		{Number: 5, Type: Fixed32, Cardinality: Repeated},
		{Number: 6, Type: Message, Cardinality: Singular, Nested: nested},
		{Number: 7, Type: Group, Cardinality: Singular, Nested: groupNested},
		{Number: 8, Type: Bytes, Cardinality: Repeated},
	})
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return schema
}

func tag(num, wire int) byte { return byte(num<<3 | wire) }

// fieldTag encodes a tag that may need a multi-byte field number.
func fieldTag(num, wire int) []byte {
	return varintBytes(uint64(num<<3 | wire))
}

func varintBytes(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func fieldVarint(num int, v uint64) []byte {
	return append(fieldTag(num, 0), varintBytes(v)...)
}

func fieldFixed64(num int, v uint64) []byte {
	out := fieldTag(num, 1)
	for i := 0; i < 8; i++ {
		out = append(out, byte(v>>(8*i)))
	}
	return out
}

func fieldFixed32(num int, v uint32) []byte {
	out := fieldTag(num, 5)
	for i := 0; i < 4; i++ {
		out = append(out, byte(v>>(8*i)))
	}
	return out
}

func fieldBytes(num int, payload []byte) []byte {
	out := fieldTag(num, 2)
	out = append(out, varintBytes(uint64(len(payload)))...)
	return append(out, payload...)
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not *wireadmit.Error", err)
	}
	if e.Code != want {
		t.Fatalf("expected code %q, got %q (%v)", want, e.Code, err)
	}
}

var _ = bytes.Equal

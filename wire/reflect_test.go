package wire

import (
	"errors"
	"math"
	"strings"
	"testing"
)

type Address struct {
	Zip  uint32 `wire:"1,fixed32"`
	Code int64  `wire:"2,sint"`
}

type Person struct {
	ID      uint64   `wire:"1,varint"`
	Name    string   `wire:"2,string"`
	Scores  []int64  `wire:"3,packed,sint"`
	Addr    *Address `wire:"4,message"`
	Active  bool     `wire:"5,varint"`
	Balance int64    `wire:"6,sint"`
	Ratio   float64  `wire:"7,fixed64"`
	Tags    []uint32 `wire:"8,varint"` // repeated, non-packed
	Ignored string
}

func encodePerson() []byte {
	var addr []byte
	addr = AppendTag(addr, 1, Fixed32)
	addr = AppendFixed32(addr, 100084)
	addr = AppendTag(addr, 2, Varint)
	addr = AppendVarint(addr, ZigZagEncode(-7))

	var packed []byte
	for _, s := range []int64{3, -1, 42, math.MinInt64} {
		packed = AppendVarint(packed, ZigZagEncode(s))
	}

	var buf []byte
	buf = AppendTag(buf, 1, Varint)
	buf = AppendVarint(buf, 12345)
	buf = AppendTag(buf, 2, LengthDelimited)
	buf = AppendBytes(buf, []byte("Ada"))
	buf = AppendTag(buf, 3, LengthDelimited)
	buf = AppendBytes(buf, packed)
	buf = AppendTag(buf, 4, LengthDelimited)
	buf = AppendBytes(buf, addr)
	buf = AppendTag(buf, 5, Varint)
	buf = AppendVarint(buf, 1)
	buf = AppendTag(buf, 6, Varint)
	buf = AppendVarint(buf, ZigZagEncode(-9000))
	buf = AppendTag(buf, 7, Fixed64)
	buf = AppendFixed64(buf, math.Float64bits(2.5))
	for _, t := range []uint32{9, 8, 7} {
		buf = AppendTag(buf, 8, Varint)
		buf = AppendVarint(buf, uint64(t))
	}
	// Unknown field 99 must be skipped.
	buf = AppendTag(buf, 99, Varint)
	buf = AppendVarint(buf, 1)
	return buf
}

func TestUnmarshalNestedAndPacked(t *testing.T) {
	var p Person
	if err := Unmarshal(encodePerson(), &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != 12345 || p.Name != "Ada" || !p.Active || p.Balance != -9000 || p.Ratio != 2.5 {
		t.Fatalf("scalars: %+v", p)
	}
	if len(p.Scores) != 4 || p.Scores[0] != 3 || p.Scores[1] != -1 || p.Scores[2] != 42 || p.Scores[3] != math.MinInt64 {
		t.Fatalf("packed sint: %v", p.Scores)
	}
	if p.Addr == nil || p.Addr.Zip != 100084 || p.Addr.Code != -7 {
		t.Fatalf("nested: %+v", p.Addr)
	}
	if len(p.Tags) != 3 || p.Tags[0] != 9 || p.Tags[2] != 7 {
		t.Fatalf("repeated: %v", p.Tags)
	}
	if p.Ignored != "" {
		t.Fatalf("untagged field touched: %q", p.Ignored)
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(encodePerson(), &Person{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWireTypeMismatch(t *testing.T) {
	// Field 1 declared varint in schema, sent as fixed64.
	var buf []byte
	buf = AppendTag(buf, 1, Fixed64)
	buf = AppendFixed64(buf, 1)
	err := Validate(buf, &Person{})
	if !errors.Is(err, ErrWireTypeMismatch) {
		t.Fatalf("want ErrWireTypeMismatch, got %v", err)
	}
	var p Person
	if err := Unmarshal(buf, &p); !errors.Is(err, ErrWireTypeMismatch) {
		t.Fatalf("unmarshal: want ErrWireTypeMismatch, got %v", err)
	}
}

type Node struct {
	Next *Node  `wire:"1,message"`
	V    uint64 `wire:"2,varint"`
}

func TestValidateDepthLimit(t *testing.T) {
	// Build a self-nesting message deeper than MaxDepth.
	buf := []byte{0x10, 0x01} // leaf: field 2 varint 1
	for i := 0; i < MaxDepth+5; i++ {
		var next []byte
		next = AppendTag(next, 1, LengthDelimited)
		next = AppendBytes(next, buf)
		buf = next
	}
	if err := Validate(buf, &Node{}); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("want ErrDepthLimit, got %v", err)
	}
	var n Node
	if err := Unmarshal(buf, &n); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("unmarshal: want ErrDepthLimit, got %v", err)
	}
	// A message exactly at the limit is still fine.
	buf = []byte{0x10, 0x01}
	for i := 0; i < MaxDepth; i++ {
		var next []byte
		next = AppendTag(next, 1, LengthDelimited)
		next = AppendBytes(next, buf)
		buf = next
	}
	if err := Validate(buf, &Node{}); err != nil {
		t.Fatalf("at-limit nesting should pass: %v", err)
	}
}

func TestValidateTruncatedNested(t *testing.T) {
	full := encodePerson()
	err := Validate(full[:len(full)-2], &Person{})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestUnmarshalBadTargets(t *testing.T) {
	var p Person
	if err := Unmarshal(nil, p); !errors.Is(err, ErrBadTarget) {
		t.Fatalf("non-pointer: %v", err)
	}
	if err := Unmarshal(nil, nil); !errors.Is(err, ErrBadTarget) {
		t.Fatalf("nil: %v", err)
	}
	var pp *Person
	if err := Unmarshal(nil, pp); !errors.Is(err, ErrBadTarget) {
		t.Fatalf("nil pointer: %v", err)
	}
}

func TestBadTag(t *testing.T) {
	type Bad struct {
		X uint64 `wire:"varint,1"` // swapped
	}
	var b Bad
	err := Unmarshal([]byte{0x08, 0x01}, &b)
	if !errors.Is(err, ErrBadTag) {
		t.Fatalf("want ErrBadTag, got %v", err)
	}
}

func TestFieldErrorMessage(t *testing.T) {
	_, err := Parse([]byte{0x12, 0x05, 'a'})
	if err == nil || !strings.Contains(err.Error(), "field 2") {
		t.Fatalf("error should name the field: %v", err)
	}
}

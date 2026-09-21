package tlv

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func sampleFields() []*Field {
	return []*Field{
		NewPrimitive(0x0001, []byte("hello")),
		NewContainer(0x0002,
			NewPrimitive(0x0003, []byte{0xde, 0xad}),
			NewContainer(0x0004, NewPrimitive(0x0005, nil)),
		),
		NewPrimitive(0x7fff, []byte{0x01, 0x02, 0x03}), // unknown type
	}
}

func TestRoundTrip(t *testing.T) {
	wire := Encode(sampleFields())
	got, err := Parse(wire)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if out := Encode(got); !bytes.Equal(out, wire) {
		t.Fatalf("re-encode mismatch:\n got %x\nwant %x", out, wire)
	}
}

func TestUnknownTypePassthrough(t *testing.T) {
	// Hand-built wire with types the codec knows nothing about.
	wire := []byte{
		0x12, 0x34, 0, 0, 0, 3, 0xaa, 0xbb, 0xcc,
		0x00, 0x01, 0, 0, 0, 2, 0x00, 0xff,
		0x12, 0x34, 0, 0, 0, 1, 0x7e,
	}
	fields, err := Parse(wire)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(fields) != 3 || fields[0].Type != 0x1234 {
		t.Fatalf("unexpected fields: %+v", fields)
	}
	if out := Encode(fields); !bytes.Equal(out, wire) {
		t.Fatalf("unknown-type passthrough not byte-identical:\n got %x\nwant %x", out, wire)
	}
}

func TestNestedContainers(t *testing.T) {
	inner := NewContainer(0x0010, NewPrimitive(0x0020, []byte("x")))
	outer := NewContainer(0x0030, inner, NewPrimitive(0x0040, []byte("y")))
	fields, err := Parse(Encode([]*Field{outer}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := fields[0]
	if !IsContainer(got.Type) || len(got.Children) != 2 {
		t.Fatalf("outer: %+v", got)
	}
	if !IsContainer(got.Children[0].Type) || got.Children[0].Children[0].Value[0] != 'x' {
		t.Fatalf("inner: %+v", got.Children[0])
	}
}

func TestEmptyContainer(t *testing.T) {
	wire := Encode([]*Field{NewContainer(0x0009)})
	fields, err := Parse(wire)
	if err != nil {
		t.Fatalf("empty container must be legal: %v", err)
	}
	if len(fields) != 1 || len(fields[0].Children) != 0 {
		t.Fatalf("unexpected: %+v", fields)
	}
	if out := Encode(fields); !bytes.Equal(out, wire) {
		t.Fatalf("re-encode mismatch: %x vs %x", out, wire)
	}
}

func TestDuplicateOrderPreserved(t *testing.T) {
	fields := []*Field{
		NewPrimitive(0x0001, []byte("first")),
		NewPrimitive(0x0001, []byte("second")),
		NewPrimitive(0x0001, []byte("third")),
	}
	got, err := Parse(Encode(fields))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for i, want := range []string{"first", "second", "third"} {
		if string(got[i].Value) != want {
			t.Fatalf("duplicate order lost at %d: %q", i, got[i].Value)
		}
	}
	// Canonical sort must be stable for equal types.
	canon, err := Parse(EncodeCanonical(fields))
	if err != nil {
		t.Fatalf("Parse canonical: %v", err)
	}
	for i, want := range []string{"first", "second", "third"} {
		if string(canon[i].Value) != want {
			t.Fatalf("canonical order unstable at %d: %q", i, canon[i].Value)
		}
	}
}

func TestCanonicalSerialization(t *testing.T) {
	fields := []*Field{
		NewPrimitive(0x0003, []byte("c")),
		NewPrimitive(0x0001, []byte("a2")),
		NewPrimitive(0x0001, []byte("a1")),
		NewContainer(0x0002, NewPrimitive(0x0009, nil), NewPrimitive(0x0008, nil)),
	}
	canon := EncodeCanonical(fields)
	want := []byte{
		0x00, 0x01, 0, 0, 0, 2, 'a', '2',
		0x00, 0x01, 0, 0, 0, 2, 'a', '1',
		0x00, 0x03, 0, 0, 0, 1, 'c',
		0x80, 0x02, 0, 0, 0, 12,
		0x00, 0x08, 0, 0, 0, 0,
		0x00, 0x09, 0, 0, 0, 0,
	}
	if !bytes.Equal(canon, want) {
		t.Fatalf("canonical encoding:\n got %x\nwant %x", canon, want)
	}
}

func TestChildOverrunsParentReportsAbsoluteOffset(t *testing.T) {
	// Top-level padding field (6+2 bytes), then a container whose child
	// claims 10 value bytes while only 3 remain inside the parent.
	wire := []byte{
		0x00, 0x01, 0, 0, 0, 2, 0xaa, 0xaa, // offset 0: padding
		0x80, 0x02, 0, 0, 0, 9, // offset 8: container, 9-byte value
		0x00, 0x03, 0, 0, 0, 10, 0x01, 0x02, 0x03, // offset 14: greedy child
	}
	_, err := Parse(wire)
	var de *Error
	if !errors.As(err, &de) {
		t.Fatalf("want *Error, got %v", err)
	}
	if de.Kind != KindLengthOverflow {
		t.Fatalf("kind = %v, want KindLengthOverflow (%v)", de.Kind, de)
	}
	if de.Offset != 14 {
		t.Fatalf("offset = %d, want absolute 14 (%v)", de.Offset, de)
	}
	if errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("boundary violation must not look like truncation: %v", err)
	}
}

func TestTruncatedInputIsNeedMoreData(t *testing.T) {
	wire := Encode(sampleFields())
	for _, cut := range []int{1, HeaderLen - 1, HeaderLen, len(wire) - 1} {
		_, err := Parse(wire[:cut])
		if !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("cut=%d: want ErrNeedMoreData, got %v", cut, err)
		}
	}
}

func TestStreamingArbitraryChunks(t *testing.T) {
	wire := Encode(sampleFields())
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		p := NewParser()
		for rest := wire; len(rest) > 0; {
			n := 1 + rng.Intn(len(rest))
			if err := p.Feed(rest[:n]); err != nil {
				t.Fatalf("Feed: %v", err)
			}
			rest = rest[n:]
		}
		got, err := p.Finalize()
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if out := Encode(got); !bytes.Equal(out, wire) {
			t.Fatalf("trial %d: chunked parse mismatch", trial)
		}
	}
}

func TestStreamingByteByByte(t *testing.T) {
	wire := Encode(sampleFields())
	p := NewParser()
	for _, b := range wire {
		if err := p.Feed([]byte{b}); err != nil {
			t.Fatalf("Feed: %v", err)
		}
	}
	got, err := p.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if out := Encode(got); !bytes.Equal(out, wire) {
		t.Fatalf("byte-by-byte parse mismatch")
	}
}

func TestStreamingFinalizeReportsTruncation(t *testing.T) {
	wire := Encode(sampleFields())
	p := NewParser()
	if err := p.Feed(wire[:len(wire)-2]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if _, err := p.Finalize(); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("want ErrNeedMoreData, got %v", err)
	}
}

func TestQuotaCheckedBeforeAllocation(t *testing.T) {
	p := &Parser{MaxValueLen: 8}
	// Header claims 1<<30 bytes; only the header is ever fed.
	err := p.Feed([]byte{0x00, 0x01, 0x40, 0x00, 0x00, 0x00})
	var de *Error
	if !errors.As(err, &de) || de.Kind != KindQuotaExceeded {
		t.Fatalf("want KindQuotaExceeded, got %v", err)
	}
	if de.Offset != 0 {
		t.Fatalf("offset = %d, want 0", de.Offset)
	}
	// Sticky: further feeds keep failing.
	if p.Feed([]byte{0x00}) == nil {
		t.Fatalf("expected sticky error")
	}
}

func TestNestedQuotaExceeded(t *testing.T) {
	wire := []byte{
		0x80, 0x01, 0, 0, 0, 6, // container, 6-byte value
		0x00, 0x02, 0, 0, 0, 0, // child claims 0-length... replaced below
	}
	// Child claims 100 bytes with quota 10 inside the parent.
	wire = []byte{
		0x80, 0x01, 0, 0, 0, 6,
		0x00, 0x02, 0, 0, 0, 100,
	}
	p := &Parser{MaxValueLen: 10}
	err := p.Feed(wire)
	var de *Error
	if !errors.As(err, &de) || de.Kind != KindQuotaExceeded {
		t.Fatalf("want KindQuotaExceeded, got %v", err)
	}
	if de.Offset != 6 {
		t.Fatalf("offset = %d, want absolute 6", de.Offset)
	}
}

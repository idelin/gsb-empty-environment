package tlv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// build is a test helper that encodes one TLV from raw parts.
func build(typ uint32, payload []byte) []byte {
	var b []byte
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], typ)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	b = append(b, hdr[:]...)
	return append(b, payload...)
}

func TestRoundTripPrimitive(t *testing.T) {
	in := &Value{Type: 7, Raw: []byte("hello")}
	enc := in.Encode()
	v, n, err := Parse(enc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if n != len(enc) {
		t.Fatalf("consumed %d, want %d", n, len(enc))
	}
	if v.Type != 7 || !bytes.Equal(v.Raw, []byte("hello")) {
		t.Fatalf("got %+v", v)
	}
	if !bytes.Equal(v.Encode(), enc) {
		t.Fatal("re-encode mismatch")
	}
}

func TestUnknownTypePassthrough(t *testing.T) {
	// 0x5EADBEEF is an unknown, non-container type with opaque payload.
	raw := build(0x5EADBEEF, []byte{0x00, 0xFF, 0x10, 0x80, 0x7F})
	v, _, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v.IsContainer() {
		t.Fatal("unknown type must not be treated as container")
	}
	if !bytes.Equal(v.Encode(), raw) {
		t.Fatalf("unknown field not byte-identical after decode/encode:\n got %x\nwant %x", v.Encode(), raw)
	}
}

func TestNestedContainersCanonical(t *testing.T) {
	leaf1 := &Value{Type: 1, Raw: []byte{0xAA}}
	leaf2 := &Value{Type: 2, Raw: []byte{0xBB, 0xCC}}
	inner := &Value{Type: ContainerFlag | 0x10, Children: []*Value{leaf1, leaf2}}
	outer := &Value{Type: ContainerFlag | 0x01, Children: []*Value{inner, leaf1}}

	enc := outer.Encode()
	// Canonical: declared lengths equal exact payload sizes.
	if got := binary.BigEndian.Uint32(enc[4:8]); got != uint32(len(enc)-HeaderSize) {
		t.Fatalf("outer length %d, want %d", got, len(enc)-HeaderSize)
	}
	back, n, err := Parse(enc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if n != len(enc) {
		t.Fatalf("consumed %d, want %d", n, len(enc))
	}
	if !bytes.Equal(back.Encode(), enc) {
		t.Fatal("nested round-trip mismatch")
	}
	if back.Children[0].Children[1].Type != 2 {
		t.Fatalf("nested child lost: %+v", back.Children[0])
	}
}

func TestDuplicateFieldsKeepOrder(t *testing.T) {
	payload := append(build(5, []byte("first")), build(5, []byte("second"))...)
	payload = append(payload, build(5, []byte("third"))...)
	wire := build(ContainerFlag|0x20, payload)

	v, _, err := Parse(wire)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(v.Children) != 3 {
		t.Fatalf("got %d children, want 3", len(v.Children))
	}
	for i, want := range []string{"first", "second", "third"} {
		if string(v.Children[i].Raw) != want {
			t.Fatalf("child %d = %q, want %q", i, v.Children[i].Raw, want)
		}
	}
	if !bytes.Equal(v.Encode(), wire) {
		t.Fatal("duplicate order not preserved on re-encode")
	}
}

func TestEmptyContainer(t *testing.T) {
	wire := build(ContainerFlag|0x30, nil)
	v, n, err := Parse(wire)
	if err != nil {
		t.Fatalf("empty container must be legal: %v", err)
	}
	if n != HeaderSize || !v.IsContainer() || len(v.Children) != 0 {
		t.Fatalf("got %+v n=%d", v, n)
	}
	if !bytes.Equal(v.Encode(), wire) {
		t.Fatal("empty container round-trip mismatch")
	}
}

func TestChildOverrunReportsAbsoluteOffset(t *testing.T) {
	// Stream: [primitive 4-byte payload][container with overrunning child].
	// Primitive occupies offsets 0..11, container header at 12, child header
	// at absolute offset 20; child declares 10 bytes but only 3 remain.
	prefix := build(1, []byte("abcd"))
	child := build(2, nil) // type=2, length overwritten below
	binary.BigEndian.PutUint32(child[4:8], 10)
	badPayload := append(child, 0x01, 0x02, 0x03) // 3 of 10 bytes present
	stream := append(prefix, build(ContainerFlag|1, badPayload)...)

	d := &Decoder{}
	d.Write(stream)
	if _, err := d.Next(); err != nil {
		t.Fatalf("first value: %v", err)
	}
	_, err := d.Next()
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("want *CorruptError, got %v", err)
	}
	if ce.Offset != 20 {
		t.Fatalf("offset = %d, want absolute 20", ce.Offset)
	}
	if errors.Is(err, ErrIncomplete) {
		t.Fatal("overrun inside parent must be corrupt, not incomplete")
	}
	t.Logf("error: %v", err)
}

func TestChunkedDecoder(t *testing.T) {
	inner := &Value{Type: ContainerFlag | 1, Children: []*Value{
		{Type: 9, Raw: []byte("chunked")},
		{Type: 0x7777, Raw: []byte{1, 2, 3}},
	}}
	wire := inner.Encode()

	d := &Decoder{}
	var got *Value
	for i, b := range wire {
		d.Write([]byte{b}) // one byte at a time
		v, err := d.Next()
		if i < len(wire)-1 {
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("byte %d: want ErrIncomplete, got %v", i, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("final byte: %v", err)
		}
		got = v
	}
	if got == nil || !bytes.Equal(got.Encode(), wire) {
		t.Fatal("chunked decode mismatch")
	}
}

func TestIncompleteVsCorrupt(t *testing.T) {
	wire := build(3, []byte("payload-data"))
	// Truncations are incomplete, never corrupt.
	for _, n := range []int{0, 3, HeaderSize - 1, HeaderSize, len(wire) - 1} {
		if _, _, err := Parse(wire[:n]); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("truncation to %d: want ErrIncomplete, got %v", n, err)
		}
	}
	// Trailing garbage inside a container is corrupt.
	bad := build(ContainerFlag|1, []byte{0x01, 0x02, 0x03})
	if _, _, err := Parse(bad); errors.Is(err, ErrIncomplete) {
		t.Fatal("trailing garbage inside container must be corrupt")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) || ce.Offset != HeaderSize {
			t.Fatalf("want CorruptError at offset %d, got %v", HeaderSize, err)
		}
	}
}

func TestLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	// Absurd declared length with no payload: rejected by quota immediately.
	huge := make([]byte, HeaderSize)
	binary.BigEndian.PutUint32(huge[0:4], 1)
	binary.BigEndian.PutUint32(huge[4:8], 0xFFFFFFFF)
	if _, _, err := Parse(huge); errors.Is(err, ErrIncomplete) {
		t.Fatal("length above quota must be corrupt, not incomplete")
	} else {
		var ce *CorruptError
		if !errors.As(err, &ce) {
			t.Fatalf("want *CorruptError, got %v", err)
		}
	}
	// Large but in-quota declared length with short payload: incomplete,
	// and Parse must return without allocating the payload.
	big := make([]byte, HeaderSize)
	binary.BigEndian.PutUint32(big[0:4], 1)
	binary.BigEndian.PutUint32(big[4:8], 1<<20)
	if _, _, err := Parse(big); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
}

func TestMultipleValuesInStream(t *testing.T) {
	wire := append(build(1, []byte("one")), build(2, []byte("two"))...)
	d := &Decoder{}
	d.Write(wire)
	for _, want := range []string{"one", "two"} {
		v, err := d.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if string(v.Raw) != want {
			t.Fatalf("got %q, want %q", v.Raw, want)
		}
	}
	if _, err := d.Next(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("drained decoder: want ErrIncomplete, got %v", err)
	}
}

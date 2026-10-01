package wire

import (
	"bytes"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1<<21 - 1, 1 << 21, 1<<35 + 7, 1<<63 - 1, 1 << 63, ^uint64(0)}
	for _, want := range cases {
		var e Encoder
		e.Varint(want)
		d := NewDecoder(e.Bytes())
		got, err := d.Varint()
		if err != nil {
			t.Fatalf("Varint(%d): %v", want, err)
		}
		if got != want {
			t.Fatalf("Varint round trip: got %d want %d", got, want)
		}
		if d.More() {
			t.Fatalf("Varint(%d): %d trailing bytes", want, d.Remaining())
		}
	}
}

func TestVarintCanonicalVectors(t *testing.T) {
	// Vectors from the protobuf encoding spec.
	vectors := []struct {
		in   []byte
		want uint64
	}{
		{[]byte{0x00}, 0},
		{[]byte{0x01}, 1},
		{[]byte{0x96, 0x01}, 150},
		{[]byte{0xac, 0x02}, 300},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, ^uint64(0)},
	}
	for _, v := range vectors {
		got, err := NewDecoder(v.in).Varint()
		if err != nil || got != v.want {
			t.Fatalf("Varint(%x) = %d, %v; want %d", v.in, got, err, v.want)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: exceeds 64 bits.
	buf := bytes.Repeat([]byte{0xff}, 10)
	buf = append(buf, 0x01)
	if _, err := NewDecoder(buf).Varint(); err != ErrOverflow {
		t.Fatalf("expected ErrOverflow, got %v", err)
	}
	// 10th byte > 1 also overflows.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}
	if _, err := NewDecoder(buf).Varint(); err != ErrOverflow {
		t.Fatalf("expected ErrOverflow for 10th byte >1, got %v", err)
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		v int64
		u uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{2147483647, 4294967294}, {-2147483648, 4294967295},
		{1<<63 - 1, ^uint64(0) - 1}, {-1 << 63, ^uint64(0)},
	}
	for _, c := range cases {
		if got := Zigzag(c.v); got != c.u {
			t.Fatalf("Zigzag(%d) = %d, want %d", c.v, got, c.u)
		}
		if got := Unzigzag(c.u); got != c.v {
			t.Fatalf("Unzigzag(%d) = %d, want %d", c.u, got, c.v)
		}
		var e Encoder
		e.ZigZag(c.v)
		got, err := NewDecoder(e.Bytes()).ZigZag()
		if err != nil || got != c.v {
			t.Fatalf("ZigZag stream round trip: got %d, %v want %d", got, err, c.v)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	var e Encoder
	e.Fixed32(0xdeadbeef)
	e.Fixed64(0x0123456789abcdef)
	d := NewDecoder(e.Bytes())
	f32, err := d.Fixed32()
	if err != nil || f32 != 0xdeadbeef {
		t.Fatalf("Fixed32 = %#x, %v", f32, err)
	}
	f64, err := d.Fixed64()
	if err != nil || f64 != 0x0123456789abcdef {
		t.Fatalf("Fixed64 = %#x, %v", f64, err)
	}
}

func TestTruncationSafety(t *testing.T) {
	// Build a buffer exercising every primitive, then verify that every
	// truncation prefix fails cleanly with ErrTruncated (never panics, never
	// reads out of bounds).
	var e Encoder
	e.Tag(1, WTVarint)
	e.Varint(300)
	e.Tag(2, WTFixed64)
	e.Fixed64(0x0123456789abcdef)
	e.Tag(3, WTBytes)
	e.AppendBytes([]byte("hello, nested world"))
	e.Tag(4, WTFixed32)
	e.Fixed32(0xdeadbeef)
	full := e.Bytes()

	// Sanity: the full buffer decodes.
	d := NewDecoder(full)
	for d.More() {
		_, wt, err := d.Tag()
		if err != nil {
			t.Fatalf("full buffer tag: %v", err)
		}
		if err := d.Skip(wt); err != nil {
			t.Fatalf("full buffer skip: %v", err)
		}
	}

	// Every strict prefix must produce ErrTruncated somewhere and no panic.
	for n := 0; n < len(full); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix len %d panicked: %v", n, r)
				}
			}()
			d := NewDecoder(full[:n])
			for d.More() {
				_, wt, err := d.Tag()
				if err == ErrTruncated {
					return
				}
				if err != nil {
					t.Fatalf("prefix len %d: unexpected tag error %v", n, err)
				}
				if err := d.Skip(wt); err != nil {
					if err != ErrTruncated {
						t.Fatalf("prefix len %d: unexpected skip error %v", n, err)
					}
					return
				}
			}
		}()
	}
}

func TestBytesLengthOverrun(t *testing.T) {
	// Length prefix claims 1000 bytes but only 3 follow: must report
	// truncation instead of reading out of bounds.
	buf := append([]byte{0xe8, 0x07}, 'a', 'b', 'c') // varint 1000, then 3 bytes
	if _, err := NewDecoder(buf).Bytes(); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
	// Huge length prefix must not overflow int arithmetic.
	var e Encoder
	e.Varint(^uint64(0))
	if _, err := NewDecoder(e.Bytes()).Bytes(); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated for huge length, got %v", err)
	}
}

func TestTagRejectsBadWireType(t *testing.T) {
	var e Encoder
	e.Varint(uint64(1)<<3 | 7) // wire type 7 is reserved
	if _, _, err := NewDecoder(e.Bytes()).Tag(); err != ErrBadWireType {
		t.Fatalf("expected ErrBadWireType, got %v", err)
	}
	e.Reset()
	e.Varint(0) // field number 0 is illegal
	if _, _, err := NewDecoder(e.Bytes()).Tag(); err != ErrBadWireType {
		t.Fatalf("expected ErrBadWireType for field 0, got %v", err)
	}
}

func TestSkipUnknownFields(t *testing.T) {
	var e Encoder
	e.Tag(1, WTVarint)
	e.Varint(42)
	e.Tag(2, WTFixed64)
	e.Fixed64(9)
	e.Tag(3, WTBytes)
	e.AppendBytes([]byte("skip me"))
	e.Tag(4, WTFixed32)
	e.Fixed32(7)
	d := NewDecoder(full(e.Bytes()))
	for d.More() {
		_, wt, err := d.Tag()
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Skip(wt); err != nil {
			t.Fatal(err)
		}
	}
}

func full(b []byte) []byte { return b }

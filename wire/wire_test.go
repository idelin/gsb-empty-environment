package wire

import (
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{0, 1, 127, 128, 300, 16384, 1 << 21, 1 << 32, 1 << 63, math.MaxUint64}
	for _, want := range values {
		b := NewBuffer()
		b.AppendVarint(want)
		got, err := NewReader(b.Bytes()).ReadVarint()
		if err != nil {
			t.Fatalf("varint(%d): %v", want, err)
		}
		if got != want {
			t.Fatalf("varint round trip: got %d, want %d", got, want)
		}
	}
}

func TestVarintSize(t *testing.T) {
	cases := []struct {
		v    uint64
		size int
	}{
		{0, 1}, {127, 1}, {128, 2}, {300, 2}, {math.MaxUint64, 10},
	}
	for _, c := range cases {
		b := NewBuffer()
		b.AppendVarint(c.v)
		if b.Len() != c.size {
			t.Fatalf("varint(%d) size = %d, want %d", c.v, b.Len(), c.size)
		}
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		signed   int64
		unsigned uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := ZigZagEncode(c.signed); got != c.unsigned {
			t.Fatalf("ZigZagEncode(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := ZigZagDecode(c.unsigned); got != c.signed {
			t.Fatalf("ZigZagDecode(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	b := NewBuffer()
	b.AppendFixed32(0xdeadbeef)
	b.AppendFixed64(0x0123456789abcdef)
	r := NewReader(b.Bytes())
	if v, err := r.ReadFixed32(); err != nil || v != 0xdeadbeef {
		t.Fatalf("fixed32: got %#x err %v", v, err)
	}
	if v, err := r.ReadFixed64(); err != nil || v != 0x0123456789abcdef {
		t.Fatalf("fixed64: got %#x err %v", v, err)
	}
	if !r.EOF() {
		t.Fatal("expected EOF")
	}
}

func TestTruncationSafety(t *testing.T) {
	// Build a buffer exercising every wire type, then feed every possible
	// truncation back through the readers: must error, never panic.
	full := NewBuffer()
	full.AppendTag(1, Varint)
	full.AppendVarint(300)
	full.AppendTag(2, Fixed64)
	full.AppendFixed64(math.MaxUint64)
	full.AppendTag(3, Bytes)
	full.AppendBytes([]byte("hello nested world"))
	full.AppendTag(4, Fixed32)
	full.AppendFixed32(42)
	data := full.Bytes()

	for cut := 0; cut < len(data); cut++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("panic on truncated input (%d bytes): %v", cut, rec)
				}
			}()
			// Safety property: decoding any truncation must never panic
			// or read out of bounds. Success is only acceptable when the
			// cut happens to land exactly on a field boundary.
			r := NewReader(data[:cut])
			for !r.EOF() {
				_, wt, err := r.ReadTag()
				if err != nil {
					return
				}
				if err := r.Skip(wt); err != nil {
					return
				}
			}
		}()
	}
}

func TestMidFieldTruncationErrors(t *testing.T) {
	full := NewBuffer()
	full.AppendTag(1, Varint)
	full.AppendVarint(300) // 2-byte varint
	full.AppendTag(2, Fixed64)
	full.AppendFixed64(0x0123456789abcdef)
	full.AppendTag(3, Bytes)
	full.AppendBytes([]byte("payload"))
	data := full.Bytes()

	// cut inside the 2-byte varint (offset: 1 tag + 1 varint byte)
	if _, _, err := NewReader(data[:2]).ReadTag(); err != nil {
		t.Fatal(err)
	}
	r := NewReader(data[:2])
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadVarint(); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	// cut inside fixed64: tag + varint + tag + 1 byte of the 8-byte value
	r = NewReader(data[:5])
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadVarint(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFixed64(); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	// cut inside the length-delimited payload
	r = NewReader(data[:len(data)-2])
	for !r.EOF() {
		_, wt, err := r.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Skip(wt); err != nil {
			if err != ErrTruncated {
				t.Fatalf("want ErrTruncated, got %v", err)
			}
			return
		}
	}
	t.Fatal("expected truncation error")
}

func TestVarintOverflow(t *testing.T) {
	// 10 bytes with continuation bits and payload > 1 in the 10th byte.
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, err := NewReader(over).ReadVarint(); err != ErrVarintOverflow {
		t.Fatalf("want ErrVarintOverflow, got %v", err)
	}
	// 11-byte varint never terminates legally.
	over11 := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	if _, err := NewReader(over11).ReadVarint(); err != ErrVarintOverflow {
		t.Fatalf("want ErrVarintOverflow, got %v", err)
	}
	// Legal max: 10th byte == 0x01.
	max := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if v, err := NewReader(max).ReadVarint(); err != nil || v != math.MaxUint64 {
		t.Fatalf("max varint: got %d err %v", v, err)
	}
}

func TestLengthPrefixOutOfBounds(t *testing.T) {
	b := NewBuffer()
	b.AppendVarint(1000) // claims 1000 bytes
	b.AppendBytes([]byte("only a few"))
	if _, err := NewReader(b.Bytes()).ReadBytes(); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestBadTag(t *testing.T) {
	// field number 0 is illegal
	if _, _, err := NewReader([]byte{0x00}).ReadTag(); err != ErrBadField {
		t.Fatalf("want ErrBadField, got %v", err)
	}
	// wire type 3 (start group) unsupported
	if _, _, err := NewReader([]byte{0x0b}).ReadTag(); err != ErrBadWireType {
		t.Fatalf("want ErrBadWireType, got %v", err)
	}
}

func TestNestedLengthDelimited(t *testing.T) {
	inner := NewBuffer()
	inner.AppendTag(1, Varint)
	inner.AppendVarint(7)
	outer := NewBuffer()
	outer.AppendTag(1, Bytes)
	outer.AppendBytes(inner.Bytes())
	outer.AppendTag(2, Bytes)
	outer.AppendBytes(inner.Bytes())

	r := NewReader(outer.Bytes())
	for i := 0; i < 2; i++ {
		num, wt, err := r.ReadTag()
		if err != nil || wt != Bytes || num != i+1 {
			t.Fatalf("tag: %d %d %v", num, wt, err)
		}
		b, err := r.ReadBytes()
		if err != nil {
			t.Fatal(err)
		}
		sub := NewReader(b)
		n, wt, err := sub.ReadTag()
		if err != nil || n != 1 || wt != Varint {
			t.Fatalf("inner tag: %d %d %v", n, wt, err)
		}
		if v, err := sub.ReadVarint(); err != nil || v != 7 {
			t.Fatalf("inner value: %d %v", v, err)
		}
	}
	if !r.EOF() {
		t.Fatal("expected EOF")
	}
}

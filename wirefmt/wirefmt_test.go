package wirefmt

import (
	"math"
	"reflect"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 300, 16384, 1 << 21, 1 << 35, math.MaxUint64}
	for _, v := range vals {
		e := NewEncoder()
		e.AppendVarint(v)
		got, err := NewReader(e.Bytes()).ReadVarint()
		if err != nil {
			t.Fatalf("varint(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("varint round trip: want %d got %d", v, got)
		}
	}
}

func TestVarintMinimalEncodings(t *testing.T) {
	cases := map[uint64][]byte{
		0:   {0x00},
		1:   {0x01},
		127: {0x7f},
		128: {0x80, 0x01},
		300: {0xac, 0x02},
	}
	for v, want := range cases {
		e := NewEncoder()
		e.AppendVarint(v)
		if !reflect.DeepEqual(e.Bytes(), want) {
			t.Fatalf("varint(%d) = %x, want %x", v, e.Bytes(), want)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// 0x80 has the continuation bit set but no following byte.
	if _, err := NewReader([]byte{0x80}).ReadVarint(); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if _, err := NewReader(nil).ReadVarint(); err != ErrTruncated {
		t.Fatalf("empty input: want ErrTruncated, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte > 1 overflows 64 bits.
	buf := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, err := NewReader(buf).ReadVarint(); err != ErrVarintOverflow {
		t.Fatalf("want ErrVarintOverflow, got %v", err)
	}
	// 11 continuation bytes: still rejected after the 10th.
	buf = []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	if _, err := NewReader(buf).ReadVarint(); err != ErrVarintOverflow {
		t.Fatalf("11-byte varint: want ErrVarintOverflow, got %v", err)
	}
	// MaxUint64 encodes to exactly 10 bytes and must be accepted.
	e := NewEncoder()
	e.AppendVarint(math.MaxUint64)
	if len(e.Bytes()) != 10 {
		t.Fatalf("MaxUint64 should encode to 10 bytes, got %d", len(e.Bytes()))
	}
	if _, err := NewReader(e.Bytes()).ReadVarint(); err != nil {
		t.Fatalf("MaxUint64 must decode: %v", err)
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		in   int32
		want uint32
	}{{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2147483647, 4294967294}, {-2147483648, 4294967295}}
	for _, c := range cases {
		if got := EncodeZigZag32(c.in); got != c.want {
			t.Fatalf("zigzag32(%d) = %d, want %d", c.in, got, c.want)
		}
		if got := DecodeZigZag32(c.want); got != c.in {
			t.Fatalf("unzigzag32(%d) = %d, want %d", c.want, got, c.in)
		}
	}
	for _, v := range []int64{0, -1, 1, math.MaxInt64, math.MinInt64, -1234567890123} {
		if got := DecodeZigZag64(EncodeZigZag64(v)); got != v {
			t.Fatalf("zigzag64 round trip: want %d got %d", v, got)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	e := NewEncoder()
	e.AppendFixed32(7, 0xdeadbeef)
	e.AppendFixed64(8, 0x0102030405060708)
	r := NewReader(e.Bytes())
	fn, wt, err := r.ReadTag()
	if err != nil || fn != 7 || wt != WireFixed32 {
		t.Fatalf("tag1: fn=%d wt=%d err=%v", fn, wt, err)
	}
	if v, err := r.ReadFixed32(); err != nil || v != 0xdeadbeef {
		t.Fatalf("fixed32: %x %v", v, err)
	}
	fn, wt, err = r.ReadTag()
	if err != nil || fn != 8 || wt != WireFixed64 {
		t.Fatalf("tag2: fn=%d wt=%d err=%v", fn, wt, err)
	}
	if v, err := r.ReadFixed64(); err != nil || v != 0x0102030405060708 {
		t.Fatalf("fixed64: %x %v", v, err)
	}
	if !r.EOF() {
		t.Fatal("expected EOF")
	}
}

func TestFixedTruncated(t *testing.T) {
	if _, err := NewReader([]byte{1, 2, 3}).ReadFixed32(); err != ErrTruncated {
		t.Fatalf("fixed32: want ErrTruncated, got %v", err)
	}
	if _, err := NewReader([]byte{1, 2, 3, 4, 5, 6, 7}).ReadFixed64(); err != ErrTruncated {
		t.Fatalf("fixed64: want ErrTruncated, got %v", err)
	}
}

func TestInvalidTags(t *testing.T) {
	// Field number 0 is illegal.
	if _, _, err := NewReader([]byte{0x00}).ReadTag(); err != ErrInvalidFieldNum {
		t.Fatalf("field 0: want ErrInvalidFieldNum, got %v", err)
	}
	// Wire types 3, 4, 6, 7 are not supported (groups are deprecated).
	if _, _, err := NewReader([]byte{0x0b}).ReadTag(); err != ErrInvalidWireType {
		t.Fatalf("wire type 3: want ErrInvalidWireType, got %v", err)
	}
}

func TestLengthDelimitedTruncated(t *testing.T) {
	// Declares 10 bytes but only 2 follow.
	buf := []byte{0x0a, 0x0a, 'h', 'i'}
	r := NewReader(buf)
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadBytes(); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	// Absurd length prefix must not allocate or panic.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0x7f}
	if _, err := NewReader(buf).ReadBytes(); err != ErrTruncated {
		t.Fatalf("huge length: want ErrTruncated, got %v", err)
	}
}

func TestPackedFields(t *testing.T) {
	e := NewEncoder()
	e.AppendPackedVarints(1, []uint64{0, 1, 300, math.MaxUint64})
	e.AppendPackedSint32s(2, []int32{0, -1, 1, -2147483648, 2147483647})
	e.AppendPackedFixed32s(3, []uint32{7, 0xdeadbeef})
	e.AppendPackedFixed64s(4, []uint64{9, 0x0102030405060708})

	r := NewReader(e.Bytes())
	read := func(want WireType) {
		t.Helper()
		_, wt, err := r.ReadTag()
		if err != nil || wt != want {
			t.Fatalf("tag: wt=%d err=%v", wt, err)
		}
	}
	read(WireLengthDelimited)
	vs, err := r.ReadPackedVarints()
	if err != nil || !reflect.DeepEqual(vs, []uint64{0, 1, 300, math.MaxUint64}) {
		t.Fatalf("packed varints: %v %v", vs, err)
	}
	read(WireLengthDelimited)
	s32, err := r.ReadPackedSint32s()
	if err != nil || !reflect.DeepEqual(s32, []int32{0, -1, 1, -2147483648, 2147483647}) {
		t.Fatalf("packed sint32: %v %v", s32, err)
	}
	read(WireLengthDelimited)
	f32, err := r.ReadPackedFixed32s()
	if err != nil || !reflect.DeepEqual(f32, []uint32{7, 0xdeadbeef}) {
		t.Fatalf("packed fixed32: %v %v", f32, err)
	}
	read(WireLengthDelimited)
	f64, err := r.ReadPackedFixed64s()
	if err != nil || !reflect.DeepEqual(f64, []uint64{9, 0x0102030405060708}) {
		t.Fatalf("packed fixed64: %v %v", f64, err)
	}
	if !r.EOF() {
		t.Fatal("expected EOF")
	}
}

func TestPackedFixedMisaligned(t *testing.T) {
	// Packed fixed64 payload of 3 bytes cannot be whole elements.
	e := NewEncoder()
	e.AppendBytes(1, []byte{1, 2, 3})
	r := NewReader(e.Bytes())
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadPackedFixed64s(); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestSkipUnknownFields(t *testing.T) {
	e := NewEncoder()
	e.AppendUint64(1, 42)                                         // known
	e.AppendString(2, "skip me")                                  // unknown
	e.AppendFixed64(3, 0xabcdef)                                  // unknown
	e.AppendMessage(4, func(s *Encoder) { s.AppendUint64(1, 1) }) // unknown
	e.AppendFixed32(5, 9)                                         // unknown
	e.AppendUint64(6, 77)                                         // known
	known := map[int]bool{1: true, 6: true}

	var got []uint64
	r := NewReader(e.Bytes())
	for !r.EOF() {
		fn, wt, err := r.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		if known[fn] {
			v, err := r.ReadVarint()
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		} else if err := r.Skip(wt); err != nil {
			t.Fatalf("skip field %d: %v", fn, err)
		}
	}
	if !reflect.DeepEqual(got, []uint64{42, 77}) {
		t.Fatalf("got %v", got)
	}
}

// TestTruncationSafety cuts a fully valid, deeply nested buffer at every
// possible length and asserts the parser always fails cleanly with an
// error instead of panicking or reading out of bounds.
func TestTruncationSafety(t *testing.T) {
	full := buildNestedFixture()
	for cut := 0; cut < len(full); cut++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("cut=%d: parser panicked: %v", cut, rec)
				}
			}()
			if err := decodeNestedFixture(full[:cut]); err == nil {
				t.Fatalf("cut=%d: truncated input decoded without error", cut)
			}
		}()
	}
	// And the full buffer must decode successfully.
	if err := decodeNestedFixture(full); err != nil {
		t.Fatalf("full buffer: %v", err)
	}
}

// buildNestedFixture creates a 3-level nested message with packed fields:
//
//	Outer{ seq=1, inner=Inner{ name="core", vals=packed[1,2,300],
//	       leaf=Leaf{ delta=zigzag(-5), crc=fixed64 } }, tags=["a","bb"] }
//
// All fields are treated as required so that any truncated prefix is
// rejected: protobuf has no framing, so a prefix ending exactly on a
// field boundary is only detectable as invalid via missing fields.
func buildNestedFixture() []byte {
	e := NewEncoder()
	e.AppendUint64(1, 1)
	e.AppendMessage(2, func(inner *Encoder) {
		inner.AppendString(1, "core")
		inner.AppendPackedVarints(2, []uint64{1, 2, 300})
		inner.AppendMessage(3, func(leaf *Encoder) {
			leaf.AppendSint64(1, -5)
			leaf.AppendFixed64(2, 0xfeedfacecafef00d)
		})
	})
	e.AppendString(3, "a")
	e.AppendString(3, "bb")
	return e.Bytes()
}

func decodeNestedFixture(buf []byte) error {
	var sawSeq, sawInner, sawName, sawVals, sawLeaf, sawDelta, sawCRC bool
	tags := 0
	r := NewReader(buf)
	for !r.EOF() {
		fn, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		switch fn {
		case 1:
			if _, err := r.ReadVarint(); err != nil {
				return err
			}
			sawSeq = true
		case 2:
			sawInner = true
			sub, err := r.ReadMessage()
			if err != nil {
				return err
			}
			for !sub.EOF() {
				sfn, swt, err := sub.ReadTag()
				if err != nil {
					return err
				}
				switch sfn {
				case 1:
					if _, err := sub.ReadString(); err != nil {
						return err
					}
					sawName = true
				case 2:
					if _, err := sub.ReadPackedVarints(); err != nil {
						return err
					}
					sawVals = true
				case 3:
					sawLeaf = true
					leaf, err := sub.ReadMessage()
					if err != nil {
						return err
					}
					for !leaf.EOF() {
						lfn, lwt, err := leaf.ReadTag()
						if err != nil {
							return err
						}
						switch lfn {
						case 1:
							if _, err := leaf.ReadSint64(); err != nil {
								return err
							}
							sawDelta = true
						case 2:
							if _, err := leaf.ReadFixed64(); err != nil {
								return err
							}
							sawCRC = true
						default:
							if err := leaf.Skip(lwt); err != nil {
								return err
							}
						}
					}
				default:
					if err := sub.Skip(swt); err != nil {
						return err
					}
				}
			}
		case 3:
			if _, err := r.ReadString(); err != nil {
				return err
			}
			tags++
		default:
			if err := r.Skip(wt); err != nil {
				return err
			}
		}
	}
	if !sawSeq || !sawInner || !sawName || !sawVals || !sawLeaf || !sawDelta || !sawCRC || tags != 2 {
		return ErrTruncated
	}
	return nil
}

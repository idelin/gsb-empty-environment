package wire

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{
		0, 1, 2, 127, 128, 129, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28,
		1<<35 - 1, 1 << 35, 1<<42 - 1, 1 << 42,
		1<<49 - 1, 1 << 49, 1<<56 - 1, 1 << 56,
		1<<63 - 1, 1 << 63, math.MaxUint64 - 1, math.MaxUint64,
	}
	for _, want := range values {
		buf := AppendVarint(nil, want)
		r := NewReader(buf)
		got, err := r.ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", want, err)
		}
		if got != want {
			t.Fatalf("ReadVarint = %d, want %d", got, want)
		}
		if !r.Done() {
			t.Fatalf("value %d left %d unread bytes", want, r.Remaining())
		}
	}
}

func TestVarintCanonicalSizes(t *testing.T) {
	cases := []struct {
		v    uint64
		size int
	}{
		{0, 1}, {127, 1}, {128, 2}, {300, 2}, {16383, 2}, {16384, 3},
		{math.MaxUint32, 5}, {math.MaxUint64, 10},
	}
	for _, c := range cases {
		if got := len(AppendVarint(nil, c.v)); got != c.size {
			t.Errorf("varint size of %d = %d, want %d", c.v, got, c.size)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// A varint with the continuation bit set but no following byte.
	r := NewReader([]byte{0x80})
	if _, err := r.ReadVarint(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	// Multi-byte varint cut short.
	r = NewReader([]byte{0xff, 0xff, 0xff})
	if _, err := r.ReadVarint(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	// Empty input.
	r = NewReader(nil)
	if _, err := r.ReadVarint(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10 continuation bytes plus a terminator: 11 bytes total.
	buf := bytes.Repeat([]byte{0xff}, 10)
	buf = append(buf, 0x01)
	r := NewReader(buf)
	if _, err := r.ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("err = %v, want ErrOverflow", err)
	}
	// 10th byte carries more than 1 payload bit.
	buf = append(bytes.Repeat([]byte{0x80}, 9), 0x02)
	r = NewReader(buf)
	if _, err := r.ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("err = %v, want ErrOverflow", err)
	}
	// 10th byte == 0x01 is the legal maximum (MaxUint64).
	buf = append(bytes.Repeat([]byte{0xff}, 9), 0x01)
	r = NewReader(buf)
	v, err := r.ReadVarint()
	if err != nil || v != math.MaxUint64 {
		t.Fatalf("max varint = %d, %v; want %d, nil", v, err, uint64(math.MaxUint64))
	}
}

func TestZigZagMapping(t *testing.T) {
	cases := []struct {
		decoded int64
		encoded uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt32, 2*math.MaxInt32 - 1 + 1},
		{math.MinInt64, math.MaxUint64},
		{math.MaxInt64, math.MaxUint64 - 1},
	}
	for _, c := range cases {
		buf := AppendZigZag(nil, c.decoded)
		r := NewReader(buf)
		enc, err := r.ReadVarint()
		if err != nil {
			t.Fatal(err)
		}
		if enc != c.encoded {
			t.Errorf("zigzag(%d) encoded = %d, want %d", c.decoded, enc, c.encoded)
		}
		r = NewReader(buf)
		dec, err := r.ReadZigZag()
		if err != nil {
			t.Fatal(err)
		}
		if dec != c.decoded {
			t.Errorf("zigzag roundtrip = %d, want %d", dec, c.decoded)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	r := NewReader(AppendFixed32(nil, 0xdeadbeef))
	if v, err := r.ReadFixed32(); err != nil || v != 0xdeadbeef {
		t.Fatalf("fixed32 = %#x, %v", v, err)
	}
	r = NewReader(AppendFixed64(nil, 0x0123456789abcdef))
	if v, err := r.ReadFixed64(); err != nil || v != 0x0123456789abcdef {
		t.Fatalf("fixed64 = %#x, %v", v, err)
	}
	// Truncation guards.
	for n := 0; n < 4; n++ {
		r = NewReader(make([]byte, n))
		if _, err := r.ReadFixed32(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed32 with %d bytes: err = %v, want ErrTruncated", n, err)
		}
	}
	for n := 0; n < 8; n++ {
		r = NewReader(make([]byte, n))
		if _, err := r.ReadFixed64(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed64 with %d bytes: err = %v, want ErrTruncated", n, err)
		}
	}
}

func TestBytesRoundTripAndBounds(t *testing.T) {
	payload := []byte("hello protobuf")
	buf := AppendBytes(nil, payload)
	r := NewReader(buf)
	got, err := r.ReadBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}

	// Length prefix claims more bytes than remain: must error, not panic.
	bad := AppendVarint(nil, 1000)
	bad = append(bad, 0x01, 0x02)
	r = NewReader(bad)
	if _, err := r.ReadBytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}

	// Huge length prefix on empty body.
	bad = AppendVarint(nil, math.MaxUint32)
	r = NewReader(bad)
	if _, err := r.ReadBytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}

	// Truncated length prefix itself.
	r = NewReader([]byte{0x80})
	if _, err := r.ReadBytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestTag(t *testing.T) {
	buf := AppendTag(nil, 15, WireBytes)
	r := NewReader(buf)
	num, wt, err := r.ReadTag()
	if err != nil {
		t.Fatal(err)
	}
	if num != 15 || wt != WireBytes {
		t.Fatalf("tag = (%d, %d), want (15, %d)", num, wt, WireBytes)
	}

	// Field number 0 is illegal.
	r = NewReader([]byte{0x00})
	if _, _, err := r.ReadTag(); !errors.Is(err, ErrInvalidFieldNumber) {
		t.Fatalf("err = %v, want ErrInvalidFieldNumber", err)
	}

	// Reserved wire types 3, 4, 6, 7 are rejected.
	for _, wt := range []int{3, 4, 6, 7} {
		r = NewReader(AppendTag(nil, 1, wt))
		if _, _, err := r.ReadTag(); !errors.Is(err, ErrInvalidWireType) {
			t.Fatalf("wire type %d: err = %v, want ErrInvalidWireType", wt, err)
		}
	}
}

func TestSkipValue(t *testing.T) {
	var buf []byte
	buf = AppendVarint(buf, 42)                  // varint
	buf = AppendFixed64(buf, 0x0102030405060708) // fixed64
	buf = AppendBytes(buf, []byte("skip me"))    // bytes
	buf = AppendFixed32(buf, 0xaabbccdd)         // fixed32
	buf = AppendVarint(buf, 777)                 // trailing value
	r := NewReader(buf)
	for _, wt := range []int{WireVarint, WireFixed64, WireBytes, WireFixed32} {
		if err := r.SkipValue(wt); err != nil {
			t.Fatal(err)
		}
	}
	v, err := r.ReadVarint()
	if err != nil || v != 777 {
		t.Fatalf("after skips: v = %d, err = %v; want 777, nil", v, err)
	}
	if err := r.SkipValue(7); !errors.Is(err, ErrInvalidWireType) {
		t.Fatalf("err = %v, want ErrInvalidWireType", err)
	}
}

// TestTruncationEveryPosition feeds every strict prefix of a valid stream to
// the readers and requires an error (never a panic) for each.
func TestTruncationEveryPosition(t *testing.T) {
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 300)
	buf = AppendTag(buf, 2, WireFixed64)
	buf = AppendFixed64(buf, math.MaxUint64)
	buf = AppendTag(buf, 3, WireBytes)
	buf = AppendBytes(buf, []byte("nested payload"))
	buf = AppendTag(buf, 4, WireFixed32)
	buf = AppendFixed32(buf, 0xffffffff)

	// Field boundaries in buf: prefixes ending exactly on a boundary are
	// valid messages; prefixes cutting a field in half must error. No
	// prefix may ever panic.
	boundaries := map[int]bool{0: true, 3: true, 12: true, 28: true, 33: true}
	for i := 0; i <= len(buf); i++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("prefix %d/%d panicked: %v", i, len(buf), rec)
				}
			}()
			r := NewReader(buf[:i])
			parseErr := error(nil)
			for !r.Done() {
				num, wt, err := r.ReadTag()
				if err != nil {
					parseErr = err
					break
				}
				_ = num
				if err := r.SkipValue(wt); err != nil {
					parseErr = err
					break
				}
			}
			if boundaries[i] && parseErr != nil {
				t.Fatalf("prefix %d/%d (field boundary) errored: %v", i, len(buf), parseErr)
			}
			if !boundaries[i] && parseErr == nil {
				t.Fatalf("prefix %d/%d (mid-field) parsed without error", i, len(buf))
			}
		}()
	}
}

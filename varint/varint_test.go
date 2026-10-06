package varint

import (
	"math"
	"math/big"
	"testing"
)

// refEncodeBig is an independent big.Int reference encoder: it peels
// off 7-bit groups with arbitrary-precision shifts and masks.
func refEncodeBig(v *big.Int) []byte {
	n := new(big.Int).Set(v)
	mask := big.NewInt(0x7f)
	limit := big.NewInt(0x80)
	var out []byte
	for n.Cmp(limit) >= 0 {
		out = append(out, byte(new(big.Int).And(n, mask).Int64())|0x80)
		n.Rsh(n, 7)
	}
	return append(out, byte(n.Int64()))
}

// refDecodeBig is an independent big.Int reference decoder.
func refDecodeBig(enc []byte) *big.Int {
	acc := new(big.Int)
	for i := len(enc) - 1; i >= 0; i-- {
		acc.Lsh(acc, 7)
		acc.Or(acc, big.NewInt(int64(enc[i]&0x7f)))
	}
	return acc
}

// refZigzagBig computes the ZigZag mapping with big.Int arithmetic.
func refZigzagBig(v int64) *big.Int {
	n := big.NewInt(v)
	if n.Sign() >= 0 {
		return n.Lsh(n, 1)
	}
	// -2v - 1
	n.Lsh(n, 1)
	return n.Neg(n).Sub(n, big.NewInt(1))
}

// boundaryValues returns 0, small values, and every value within one
// step of a 7-bit group boundary, plus the 64-bit extremes.
func boundaryValues() []uint64 {
	var vals []uint64
	for v := uint64(0); v < 1<<14; v++ {
		vals = append(vals, v)
	}
	for k := uint(1); k <= 64; k++ {
		base := uint64(1) << (k - 1) // highest value with k bits
		for _, d := range [3]uint64{0, 1, 2} {
			vals = append(vals, base+d-1)
		}
		top := ^uint64(0) >> (64 - k)
		for _, d := range [3]uint64{0, 1, 2} {
			vals = append(vals, top-d)
		}
	}
	vals = append(vals, math.MaxUint64)
	return vals
}

func TestEncodeMatchesBigInt(t *testing.T) {
	for _, v := range boundaryValues() {
		got := AppendUvarint(nil, v)
		want := refEncodeBig(new(big.Int).SetUint64(v))
		if !equalBytes(got, want) {
			t.Fatalf("encode %d: got %x, want %x", v, got, want)
		}
	}
}

func TestDecodeMatchesBigInt(t *testing.T) {
	for _, v := range boundaryValues() {
		enc := AppendUvarint(nil, v)
		d := NewDecoder(enc)
		got, err := d.DecodeUvarint()
		if err != nil {
			t.Fatalf("decode %x: %v", enc, err)
		}
		if ref := refDecodeBig(enc); ref.Cmp(new(big.Int).SetUint64(got)) != 0 {
			t.Fatalf("decode %x: got %d, big.Int says %s", enc, got, ref)
		}
		if got != v {
			t.Fatalf("round trip %d: got %d", v, got)
		}
		if d.Pos() != len(enc) {
			t.Fatalf("decode %x: pos %d, want %d", enc, d.Pos(), len(enc))
		}
	}
}

func TestEncodedLengthCrossingBlocks(t *testing.T) {
	// A value with b significant bits needs ceil(b/7) bytes; check at
	// and around every block (7-bit group) boundary up to 64 bits.
	for k := uint(1); k <= 64; k++ {
		for _, v := range []uint64{uint64(1)<<(k-1) - 1, uint64(1) << (k - 1)} {
			enc := AppendUvarint(nil, v)
			bits := uint64BitLen(v)
			wantLen := (bits + 6) / 7
			if wantLen == 0 {
				wantLen = 1
			}
			if len(enc) != wantLen {
				t.Fatalf("value %d (%d bits): len %d, want %d", v, bits, len(enc), wantLen)
			}
		}
	}
	// MaxUint64 must take exactly MaxLen bytes with a 1-bit top group.
	enc := AppendUvarint(nil, math.MaxUint64)
	if len(enc) != MaxLen || enc[MaxLen-1] != 0x01 {
		t.Fatalf("MaxUint64 encoding: %x", enc)
	}
}

func uint64BitLen(v uint64) int {
	n := 0
	for v != 0 {
		n++
		v >>= 1
	}
	return n
}

func TestSignedRoundTripAndZigzag(t *testing.T) {
	vals := []int64{0, 1, -1, math.MaxInt64, math.MinInt64}
	for k := uint(1); k < 64; k++ {
		vals = append(vals, 1<<k, -(1 << k), 1<<k-1, -(1<<k - 1))
	}
	for v := int64(-100000); v <= 100000; v++ {
		vals = append(vals, v)
	}
	for _, v := range vals {
		// ZigZag mapping cross-checked against big.Int.
		if got, want := Zigzag(v), refZigzagBig(v); new(big.Int).SetUint64(got).Cmp(want) != 0 {
			t.Fatalf("zigzag %d: got %d, want %s", v, got, want)
		}
		enc := AppendVarint(nil, v)
		d := NewDecoder(enc)
		got, err := d.DecodeVarint()
		if err != nil {
			t.Fatalf("decode varint %x: %v", enc, err)
		}
		if got != v {
			t.Fatalf("signed round trip %d: got %d", v, got)
		}
		if d.Pos() != len(enc) {
			t.Fatalf("signed decode %x: pos %d, want %d", enc, d.Pos(), len(enc))
		}
	}
}

func TestNonMinimalRejected(t *testing.T) {
	cases := [][]byte{
		{0x80, 0x00},                   // 0 in two bytes
		{0xff, 0x00},                   // 127 in two bytes
		{0x80, 0x80, 0x00},             // 0 in three bytes
		{0x81, 0x80, 0x80, 0x80, 0x00}, // 1 in five bytes
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, // 0 in MaxLen bytes
	}
	for _, enc := range cases {
		d := NewDecoder(enc)
		_, err := d.DecodeUvarint()
		de, ok := err.(*DecodeError)
		if !ok || de.Kind != NonMinimal {
			t.Fatalf("%x: got %v, want NonMinimal", enc, err)
		}
		if de.Start != 0 || de.End != len(enc) {
			t.Fatalf("%x: range [%d,%d), want [0,%d)", enc, de.Start, de.End, len(enc))
		}
		if d.Pos() != len(enc) {
			t.Fatalf("%x: pos %d, want resume at %d", enc, d.Pos(), len(enc))
		}
	}
}

func TestOverlongRejected(t *testing.T) {
	// Continuation bit still set on the 10th byte: 11 groups.
	enc := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	d := NewDecoder(enc)
	_, err := d.DecodeUvarint()
	de, ok := err.(*DecodeError)
	if !ok || de.Kind != Overlong {
		t.Fatalf("got %v, want Overlong", err)
	}
	if de.Start != 0 || de.End != MaxLen {
		t.Fatalf("range [%d,%d), want [0,%d)", de.Start, de.End, MaxLen)
	}
	if d.Pos() != MaxLen {
		t.Fatalf("pos %d, want %d", d.Pos(), MaxLen)
	}
}

func TestOverflowRejected(t *testing.T) {
	// Ten bytes, top group carrying 2 bits: 65 significant bits.
	enc := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	d := NewDecoder(enc)
	_, err := d.DecodeUvarint()
	de, ok := err.(*DecodeError)
	if !ok || de.Kind != Overflow {
		t.Fatalf("got %v, want Overflow", err)
	}
	if de.Start != 0 || de.End != MaxLen {
		t.Fatalf("range [%d,%d), want [0,%d)", de.Start, de.End, MaxLen)
	}
	// The largest legal 10-byte encoding is MaxUint64 (top group 0x01).
	ok10 := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	d = NewDecoder(ok10)
	v, err := d.DecodeUvarint()
	if err != nil || v != math.MaxUint64 {
		t.Fatalf("max legal 10-byte value: got %d, %v", v, err)
	}
}

func TestTruncatedResumeAtFrameBoundary(t *testing.T) {
	vals := []uint64{0, 127, 128, 300, math.MaxUint64}
	var stream []byte
	for _, v := range vals {
		stream = AppendUvarint(stream, v)
	}
	// Cut the stream in the middle of the last (10-byte) value.
	cut := len(stream) - 4
	d := NewDecoder(stream[:cut])
	for i := 0; i < len(vals)-1; i++ {
		v, err := d.DecodeUvarint()
		if err != nil || v != vals[i] {
			t.Fatalf("value %d: got %d, %v", i, v, err)
		}
	}
	boundary := d.Pos()
	_, err := d.DecodeUvarint()
	de, ok := err.(*DecodeError)
	if !ok || de.Kind != Truncated {
		t.Fatalf("got %v, want Truncated", err)
	}
	if de.Start != boundary || de.End != cut {
		t.Fatalf("range [%d,%d), want [%d,%d)", de.Start, de.End, boundary, cut)
	}
	if d.Pos() != boundary {
		t.Fatalf("truncation consumed input: pos %d, want %d", d.Pos(), boundary)
	}
	// Deliver the rest of the stream; decoding resumes at the boundary.
	d.Append(stream[cut:])
	v, err := d.DecodeUvarint()
	if err != nil || v != vals[len(vals)-1] {
		t.Fatalf("resumed decode: got %d, %v", v, err)
	}
	if d.Pos() != len(stream) {
		t.Fatalf("final pos %d, want %d", d.Pos(), len(stream))
	}
}

func TestStreamFrameBoundaries(t *testing.T) {
	vals := []int64{0, -1, 1, 127, -128, 1 << 40, -(1 << 40), math.MaxInt64, math.MinInt64}
	var stream []byte
	var offsets []int
	for _, v := range vals {
		offsets = append(offsets, len(stream))
		stream = AppendVarint(stream, v)
	}
	d := NewDecoder(stream)
	for i, want := range vals {
		if d.Pos() != offsets[i] {
			t.Fatalf("frame %d: pos %d, want boundary %d", i, d.Pos(), offsets[i])
		}
		got, err := d.DecodeVarint()
		if err != nil || got != want {
			t.Fatalf("frame %d: got %d, %v, want %d", i, got, err, want)
		}
	}
	if d.Pos() != len(stream) {
		t.Fatalf("end pos %d, want %d", d.Pos(), len(stream))
	}
	// Seeking back to an explicit boundary replays the value there.
	d.Seek(offsets[3])
	if got, _ := d.DecodeVarint(); got != vals[3] {
		t.Fatalf("seek replay: got %d, want %d", got, vals[3])
	}
}

func TestErrorRangeOffsetsInStream(t *testing.T) {
	// good value, then a bad frame, then another good value: the error
	// must report the bad frame's exact range and decoding must resume
	// right after it.
	var stream []byte
	stream = AppendUvarint(stream, 42)
	badStart := len(stream)
	stream = append(stream, 0x80, 0x00) // non-minimal zero
	badEnd := len(stream)
	stream = AppendUvarint(stream, 7)

	d := NewDecoder(stream)
	if v, err := d.DecodeUvarint(); err != nil || v != 42 {
		t.Fatalf("first value: %d, %v", v, err)
	}
	_, err := d.DecodeUvarint()
	de, ok := err.(*DecodeError)
	if !ok || de.Kind != NonMinimal {
		t.Fatalf("got %v, want NonMinimal", err)
	}
	if de.Start != badStart || de.End != badEnd {
		t.Fatalf("range [%d,%d), want [%d,%d)", de.Start, de.End, badStart, badEnd)
	}
	if v, err := d.DecodeUvarint(); err != nil || v != 7 {
		t.Fatalf("resume after bad frame: %d, %v", v, err)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

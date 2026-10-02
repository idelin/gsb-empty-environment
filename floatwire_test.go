package floatwire

import (
	"bytes"
	"math"
	"testing"
)

// All 65536 float16 patterns: encode/decode round trip in both byte
// orders, bit-exact. This covers every signed zero, infinity, NaN payload
// and subnormal the format can express.
func TestRoundTripAllFloat16Patterns(t *testing.T) {
	for _, order := range []Order{BigEndian, LittleEndian} {
		for i := 0; i < 1<<16; i++ {
			bits := uint64(i)
			enc, err := EncodeOrder(Float16, bits, order)
			if err != nil {
				t.Fatalf("EncodeOrder(%04x): %v", i, err)
			}
			if len(enc) != 2 {
				t.Fatalf("encoded length = %d, want 2", len(enc))
			}
			dec, err := DecodeOrder(Float16, enc, order)
			if err != nil {
				t.Fatalf("DecodeOrder(%04x): %v", i, err)
			}
			if dec != bits {
				t.Fatalf("round trip %04x -> %x -> %04x", i, enc, dec)
			}
		}
	}
}

// Sampled float32/float64 round trips, including all edge patterns.
func TestRoundTripWideWidths(t *testing.T) {
	edge32 := []uint64{
		0x00000000, 0x80000000, // signed zeros
		0x7F800000, 0xFF800000, // infinities
		0x00000001, 0x80000001, // smallest subnormals
		0x007FFFFF, 0x00800000, // subnormal/normal boundary
		0x7F7FFFFF,             // max finite
		0x7FC00001, 0x7FA55AA5, // quiet NaNs with payloads
		0x7F800001, 0xFFA00001, // signaling NaNs with payloads
	}
	edge64 := []uint64{
		0x0000000000000000, 0x8000000000000000,
		0x7FF0000000000000, 0xFFF0000000000000,
		0x0000000000000001, 0x8000000000000001,
		0x000FFFFFFFFFFFFF, 0x0010000000000000,
		0x7FEFFFFFFFFFFFFF,
		0x7FF8000000000001, 0x7FF5A5A5A5A5A5A5,
		0x7FF0000000000001, 0xFFF4000000000001,
	}
	for _, order := range []Order{BigEndian, LittleEndian} {
		for _, b := range edge32 {
			enc, err := EncodeOrder(Float32, b, order)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := DecodeOrder(Float32, enc, order)
			if err != nil || dec != b {
				t.Fatalf("f32 round trip %08x -> %08x (err=%v)", b, dec, err)
			}
		}
		for _, b := range edge64 {
			enc, err := EncodeOrder(Float64, b, order)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := DecodeOrder(Float64, enc, order)
			if err != nil || dec != b {
				t.Fatalf("f64 round trip %016x -> %016x (err=%v)", b, dec, err)
			}
		}
	}
}

// Endianness switching: decoding with the other order byte-swaps the
// value, and swapping back restores the exact bits.
func TestEndiannessSwitchRestores(t *testing.T) {
	values := []uint64{0x3C00, 0xBC00, 0x7E01, 0x0001, 0xFC00}
	for _, v := range values {
		be, _ := EncodeOrder(Float16, v, BigEndian)
		le, _ := EncodeOrder(Float16, v, LittleEndian)
		if be[0] != le[1] || be[1] != le[0] {
			t.Fatalf("orders are not byte-swapped for %04x", v)
		}
		// Cross-decode swaps the bytes; swapping the result restores.
		cross, err := DecodeOrder(Float16, be, LittleEndian)
		if err != nil {
			t.Fatal(err)
		}
		swapped := (v&0xFF)<<8 | (v >> 8)
		if cross != swapped {
			t.Fatalf("cross decode = %04x, want %04x", cross, swapped)
		}
		restored := (cross&0xFF)<<8 | (cross >> 8)
		if restored != v {
			t.Fatalf("restore failed: %04x", restored)
		}
	}
}

// NaN must not be misjudged by ordinary equality: host == says a NaN
// differs from itself, while Equal compares bit patterns.
func TestNaNEqualitySemantics(t *testing.T) {
	nan := uint64(0x7FF8000000000001)
	host := math.Float64frombits(nan)
	if host == host {
		t.Fatal("host NaN compared equal to itself; test assumption broken")
	}
	if !Equal(Float64, nan, nan) {
		t.Fatal("Equal must treat identical NaN patterns as equal")
	}
	other := uint64(0x7FF8000000000002)
	if Equal(Float64, nan, other) {
		t.Fatal("different NaN payloads must not compare equal")
	}
}

// Negative zero keeps its sign through encode/decode and conversions,
// and stays distinguishable from +0.
func TestNegativeZeroPreserved(t *testing.T) {
	for _, w := range []Width{Float16, Float32, Float64} {
		neg := uint64(1) << (uint(w) - 1)
		enc, err := Encode(w, neg)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := Decode(w, enc)
		if err != nil {
			t.Fatal(err)
		}
		if Sign(w, dec) != 1 {
			t.Fatalf("width %d: negative zero lost its sign", w)
		}
		if Equal(w, dec, 0) {
			t.Fatalf("width %d: -0 must differ from +0 bitwise", w)
		}
	}
	// Conversions keep the sign of zero in both modes.
	neg32, _, err := ConvertFlags(Float64, Float32, 0x8000000000000000, ModeNumeric)
	if err != nil || neg32 != 0x80000000 {
		t.Fatalf("f64->f32 -0 = %08x, err=%v", neg32, err)
	}
	neg16, _, err := ConvertFlags(Float32, Float16, 0x80000000, ModeBitPreserving)
	if err != nil || neg16 != 0x8000 {
		t.Fatalf("f32->f16 -0 = %04x, err=%v", neg16, err)
	}
	up, _, err := ConvertFlags(Float16, Float64, 0x8000, ModeNumeric)
	if err != nil || up != 0x8000000000000000 {
		t.Fatalf("f16->f64 -0 = %016x, err=%v", up, err)
	}
}

// Unsupported widths are rejected before any work happens.
func TestUnsupportedWidthRejected(t *testing.T) {
	for _, w := range []Width{0, 8, 24, 128, -16} {
		if _, err := Encode(w, 0); err != ErrUnsupportedWidth {
			t.Fatalf("Encode(%d): %v", w, err)
		}
		if _, err := Decode(w, make([]byte, 16)); err != ErrUnsupportedWidth {
			t.Fatalf("Decode(%d): %v", w, err)
		}
		if _, err := Convert(w, Float16, 0, ModeNumeric); err != ErrUnsupportedWidth {
			t.Fatalf("Convert(from=%d): %v", w, err)
		}
		if _, err := Convert(Float16, w, 0, ModeNumeric); err != ErrUnsupportedWidth {
			t.Fatalf("Convert(to=%d): %v", w, err)
		}
		if _, err := Format(w, 0); err != ErrUnsupportedWidth {
			t.Fatalf("Format(%d): %v", w, err)
		}
		if err := NewStreamEncoder(w, BigEndian).Write(0); err != ErrUnsupportedWidth {
			t.Fatalf("StreamEncoder(%d): %v", w, err)
		}
		if _, err := NewStreamDecoder(w, BigEndian).Feed(nil); err != ErrUnsupportedWidth {
			t.Fatalf("StreamDecoder(%d): %v", w, err)
		}
	}
}

// Decode validates length before reading; frames validate the declared
// width and the available length before touching the payload.
func TestLengthValidatedBeforeDecode(t *testing.T) {
	if _, err := Decode(Float16, []byte{0x3C}); err != ErrShortBuffer {
		t.Fatalf("short f16: %v", err)
	}
	if _, err := Decode(Float32, []byte{1, 2, 3}); err != ErrShortBuffer {
		t.Fatalf("short f32: %v", err)
	}
	if _, err := Decode(Float64, make([]byte, 9)); err != ErrShortBuffer {
		t.Fatalf("long f64: %v", err)
	}

	frame, err := EncodeFrame(Float32, 0x7FC00001)
	if err != nil {
		t.Fatal(err)
	}
	// Every truncation must be reported, never panic or read out of range.
	for n := 0; n < len(frame); n++ {
		if _, _, err := DecodeFrame(frame[:n]); err != ErrShortBuffer {
			t.Fatalf("truncated frame at %d: %v", n, err)
		}
	}
	bits, consumed, err := DecodeFrame(frame)
	if err != nil || bits != 0x7FC00001 || consumed != len(frame) {
		t.Fatalf("frame round trip: bits=%08x consumed=%d err=%v", bits, consumed, err)
	}
	// Bad magic and unsupported declared width.
	if _, _, err := DecodeFrame([]byte{0x00, 16, 0, 0}); err != ErrBadFrame {
		t.Fatalf("bad magic: %v", err)
	}
	if _, _, err := DecodeFrame([]byte{frameMagic, 24, 0, 0, 0}); err != ErrUnsupportedWidth {
		t.Fatalf("bad width: %v", err)
	}
}

// Arbitrary chunking of the stream yields bit-identical values.
func TestChunkedStreamBitExact(t *testing.T) {
	values := []uint64{
		0x3FF0000000000000, 0x8000000000000000, 0x7FF0000000000000,
		0xFFF0000000000000, 0x7FF8000000000001, 0x7FF0000000000001,
		0x0000000000000001, 0x000FFFFFFFFFFFFF, 0x7FEFFFFFFFFFFFFF,
		0x4014000000000000, 0xC059000000000000,
	}
	for _, order := range []Order{BigEndian, LittleEndian} {
		enc := NewStreamEncoder(Float64, order)
		for _, v := range values {
			if err := enc.Write(v); err != nil {
				t.Fatal(err)
			}
		}
		stream := enc.Bytes()
		if len(stream) != 8*len(values) {
			t.Fatalf("stream length = %d", len(stream))
		}
		for _, chunk := range []int{1, 2, 3, 5, 7, 8, 11, len(stream)} {
			dec := NewStreamDecoder(Float64, order)
			var got []uint64
			for i := 0; i < len(stream); i += chunk {
				end := i + chunk
				if end > len(stream) {
					end = len(stream)
				}
				out, err := dec.Feed(stream[i:end])
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, out...)
			}
			if err := dec.Finish(); err != nil {
				t.Fatalf("chunk %d: %v", chunk, err)
			}
			if len(got) != len(values) {
				t.Fatalf("chunk %d: got %d values", chunk, len(got))
			}
			for i, v := range values {
				if got[i] != v {
					t.Fatalf("chunk %d value %d: got %016x want %016x", chunk, i, got[i], v)
				}
			}
		}
		// A trailing partial record is an error, not silent data loss.
		dec := NewStreamDecoder(Float64, order)
		if _, err := dec.Feed(stream[:len(stream)-1]); err != nil {
			t.Fatal(err)
		}
		if err := dec.Finish(); err != ErrShortBuffer {
			t.Fatalf("dangling byte: %v", err)
		}
	}
}

// Text display is separate from storage: every float16 pattern formats
// without error, and special values render explicitly.
func TestFormatAllFloat16(t *testing.T) {
	for i := 0; i < 1<<16; i++ {
		s, err := Format(Float16, uint64(i))
		if err != nil {
			t.Fatalf("Format(%04x): %v", i, err)
		}
		if s == "" {
			t.Fatalf("Format(%04x) empty", i)
		}
	}
	checks := []struct {
		bits uint64
		want string
	}{
		{0x0000, "0"},
		{0x8000, "-0"},
		{0x7C00, "Inf"},
		{0xFC00, "-Inf"},
		{0x3C00, "1"},
		{0xC000, "-2"},
		{0x7E01, "NaN(0x1)"},
		{0x7C01, "sNaN(0x1)"},
		{0xFE55, "-NaN(0x55)"},
		{0xFC01, "-sNaN(0x1)"},
	}
	for _, c := range checks {
		got, err := Format(Float16, c.bits)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Fatalf("Format(%04x) = %q, want %q", c.bits, got, c.want)
		}
	}
}

// Encoding is canonical: the same value always produces the same bytes.
func TestEncodeCanonical(t *testing.T) {
	a, _ := Encode(Float32, 0x7FC00001)
	b, _ := Encode(Float32, 0x7FC00001)
	if !bytes.Equal(a, b) {
		t.Fatal("encoding not deterministic")
	}
	want := []byte{0x7F, 0xC0, 0x00, 0x01}
	if !bytes.Equal(a, want) {
		t.Fatalf("canonical encoding = %x, want %x", a, want)
	}
}

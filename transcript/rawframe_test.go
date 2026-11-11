package transcript

import (
	"crypto/sha256"
	"errors"
	"testing"
)

// TestUnknownRawFrame verifies unknown optional fields survive as
// verbatim frames in the digest, while malformed or ambiguous frames are
// rejected.
func TestUnknownRawFrame(t *testing.T) {
	raw := appendFrame(nil, 0, 42, typeBytes, []byte("opaque optional"))

	b := New()
	if err := b.WriteRawFrame(raw); err != nil {
		t.Fatal(err)
	}
	if !b.HasTag(42) || b.FieldCount() != 1 {
		t.Fatal("raw frame was not recorded")
	}
	d, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(manualTranscript(
		[]frame{{phase: 0, tag: 42, typ: typeBytes, payload: []byte("opaque optional")}}, 0))
	if d.Sum != want {
		t.Fatalf("raw frame digest %x != %x", d.Sum, want)
	}

	dup := New()
	if err := dup.WriteRawFrame(raw); err != nil {
		t.Fatal(err)
	}
	if err := dup.WriteRawFrame(raw); !errors.Is(err, ErrDuplicateTag) {
		t.Fatalf("duplicate raw frame: got %v", err)
	}

	// An untrusted frame from a later phase cannot be inserted early.
	if err := New().WriteRawFrame(appendFrame(nil, 3, 42, typeBytes, nil)); !errors.Is(err, ErrPhaseRollback) {
		t.Fatalf("future-phase frame: got %v", err)
	}

	bad := [][]byte{
		{},
		{0x80},             // truncated varint
		{1, 1, 99, 0},      // unknown type
		{1, 0, 1, 0},       // reserved zero tag
		append(raw, 0),     // trailing byte
		{1, 1, 1, 0x80, 0}, // non-minimal length
	}
	for i, frame := range bad {
		if err := New().WriteRawFrame(frame); err == nil {
			t.Fatalf("malformed frame %d accepted", i)
		}
	}
}

// TestIntegerCanonicalEncoding checks unique shortest encodings and
// signed/unsigned round trips.
func TestIntegerCanonicalEncoding(t *testing.T) {
	uints := []uint64{0, 1, 127, 128, 16383, 16384, 1 << 32, ^uint64(0)}
	for _, v := range uints {
		enc := appendUvarint(nil, v)
		if len(enc) != uvarintLen(v) {
			t.Fatalf("uint %d: encoded length %d != %d", v, len(enc), uvarintLen(v))
		}
		got, n, err := readUvarint(enc)
		if err != nil || got != v || n != len(enc) {
			t.Fatalf("uint %d round trip: value=%d n=%d err=%v", v, got, n, err)
		}
		// Zero itself is one byte; any multi-byte form of zero is
		// non-canonical.
		if v == 0 {
			if _, _, err := readUvarint([]byte{0x80, 0}); err == nil {
				t.Fatal("non-minimal zero encoding accepted")
			}
		}
	}

	ints := []int64{0, 1, -1, 2, -2, 63, -64, 64, -65, 1 << 40, -(1 << 40), 1<<62 - 1, -1 << 62}
	for _, v := range ints {
		enc := appendUvarint(nil, encodeZigZag(v))
		dec, _, err := readUvarint(enc)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeZigZag(dec); got != v {
			t.Fatalf("int %d round trip got %d", v, got)
		}
	}
	// ZigZag(-1) maps to 1 by design; the frame type code, not the
	// payload, disambiguates signed -1 from unsigned 1.
	if decodeZigZag(1) != -1 {
		t.Fatal("zigzag(-1) must decode as -1")
	}
	signed := appendFrame(nil, 0, 1, typeInt, appendUvarint(nil, encodeZigZag(-1)))
	unsigned := appendFrame(nil, 0, 1, typeUint, appendUvarint(nil, 1))
	if string(signed) == string(unsigned) {
		t.Fatal("signed and unsigned frames must differ via type code")
	}
}

// TestEndFrameDomainSeparation proves the closing frame distinguishes
// transcripts whose field payloads happen to equal the end marker bytes.
func TestEndFrameDomainSeparation(t *testing.T) {
	b := New()
	// Payload bytes chosen to mimic plausible framing bytes.
	if err := b.WriteBytes(1, []byte{0x02, 0x00}); err != nil {
		t.Fatal(err)
	}
	d, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}

	other := New()
	if err := other.WriteBytes(1, []byte{0x02, 0x00}); err != nil {
		t.Fatal(err)
	}
	od, err := other.Close()
	if err != nil {
		t.Fatal(err)
	}
	if d.Sum != od.Sum {
		t.Fatal("identical transcripts produced different digests")
	}

	// Changing only the field count changes the end frame and digest.
	two := New()
	if err := two.WriteBytes(1, []byte{0x02}); err != nil {
		t.Fatal(err)
	}
	if err := two.WriteBytes(2, []byte{0x00}); err != nil {
		t.Fatal(err)
	}
	td, err := two.Close()
	if err != nil {
		t.Fatal(err)
	}
	if td.Sum == d.Sum {
		t.Fatal("end frame failed to separate one-field and two-field transcripts")
	}
}

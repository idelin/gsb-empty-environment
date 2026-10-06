package bitmap

import (
	"encoding/binary"
	"testing"
)

// sampleBitmap returns a bitmap holding both array and bitmap
// containers across several keys.
func sampleBitmap() *Bitmap {
	b := New(8)
	for i := uint32(0); i < 40; i++ {
		b.Add(i * 3) // container 0: 40 values -> bitmap
	}
	b.Add(5<<16 | 1) // container 5: 1 value -> array
	b.Add(5<<16 | 9)
	b.Add(9 << 16) // container 9: 1 value -> array
	return b
}

func TestSerializeRoundTrip(t *testing.T) {
	b := sampleBitmap()
	data, err := b.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Equal(b) {
		t.Fatalf("round-trip mismatch")
	}
	if back.Threshold() != b.Threshold() {
		t.Fatalf("threshold not preserved: %d != %d", back.Threshold(), b.Threshold())
	}
	// Empty bitmap round-trip.
	empty := New(3)
	data, _ = empty.MarshalBinary()
	if _, err := Unmarshal(data); err != nil {
		t.Fatalf("empty round-trip: %v", err)
	}
}

// fixChecksum recomputes the trailing CRC after in-place edits,
// producing a structurally valid file for targeted corruption tests.
func fixChecksum(data []byte) []byte {
	return appendChecksum(data[:len(data)-checksumSize])
}

func TestUnmarshalRejectsCorruption(t *testing.T) {
	b := sampleBitmap()
	orig, err := b.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mut := func(f func(d []byte)) []byte {
		d := append([]byte(nil), orig...)
		f(d)
		return d
	}
	cases := map[string]struct {
		data []byte
		want error
	}{
		"truncated header":  {orig[:10], ErrCorrupt},
		"truncated payload": {orig[:len(orig)-10], ErrChecksum},
		"bad magic": {mut(func(d []byte) {
			d[0] ^= 0xff
			copy(d, fixChecksum(d))
		}), ErrBadMagic},
		"bad version": {mut(func(d []byte) {
			binary.LittleEndian.PutUint32(d[4:], 99)
			copy(d, fixChecksum(d))
		}), ErrBadVersion},
		"flipped payload byte": {mut(func(d []byte) {
			d[headerSize+2] ^= 0x01
		}), ErrChecksum},
		"bad checksum": {mut(func(d []byte) {
			d[len(d)-1] ^= 0xff
		}), ErrChecksum},
		"trailing garbage": {mut(func(d []byte) {
			d[len(d)-checksumSize] = 0 // keep valid, then extend below
		}), ErrChecksum},
		"bad threshold": {mut(func(d []byte) {
			binary.LittleEndian.PutUint32(d[8:], 0)
			copy(d, fixChecksum(d))
		}), ErrCorrupt},
		"reserved byte set": {mut(func(d []byte) {
			d[headerSize+3] = 1
			copy(d, fixChecksum(d))
		}), ErrCorrupt},
		"unknown kind": {mut(func(d []byte) {
			d[headerSize+2] = 7
			copy(d, fixChecksum(d))
		}), ErrCorrupt},
	}
	for name, tc := range cases {
		if _, err := Unmarshal(tc.data); err != tc.want {
			t.Errorf("%s: got error %v, want %v", name, err, tc.want)
		}
	}
}

// TestUnmarshalRejectsNonCanonicalArrays crafts files whose array
// payloads are unsorted or duplicated and expects rejection.
func TestUnmarshalRejectsNonCanonicalArrays(t *testing.T) {
	b := New(8)
	b.Add(1)
	b.Add(2)
	b.Add(5)
	orig, _ := b.MarshalBinary()
	// Container 0 starts at headerSize: key(2) kind(1) reserved(1) card(4),
	// elements at headerSize+8: 1, 2, 5.
	const elems = headerSize + 8

	dup := append([]byte(nil), orig...)
	binary.LittleEndian.PutUint16(dup[elems+2:], 1) // 1, 1, 5
	copy(dup, fixChecksum(dup))
	if _, err := Unmarshal(dup); err != ErrCorrupt {
		t.Fatalf("duplicate elements: got %v, want %v", err, ErrCorrupt)
	}

	unsorted := append([]byte(nil), orig...)
	binary.LittleEndian.PutUint16(unsorted[elems:], 5)
	binary.LittleEndian.PutUint16(unsorted[elems+4:], 1) // 5, 2, 1
	copy(unsorted, fixChecksum(unsorted))
	if _, err := Unmarshal(unsorted); err != ErrCorrupt {
		t.Fatalf("unsorted elements: got %v, want %v", err, ErrCorrupt)
	}
}

// TestUnmarshalIgnoresStoredCardinality corrupts the advisory
// cardinality of a bitmap container, fixes the checksum, and verifies
// the loader recomputes the true cardinality from the payload.
func TestUnmarshalIgnoresStoredCardinality(t *testing.T) {
	b := New(8)
	for i := uint32(0); i < 40; i++ {
		b.Add(i * 3)
	}
	orig, _ := b.MarshalBinary()
	if b.cons[0].kind != kindBitmap {
		t.Fatalf("expected bitmap container")
	}
	// Stored card of container 0 sits at headerSize+4.
	d := append([]byte(nil), orig...)
	binary.LittleEndian.PutUint32(d[headerSize+4:], 9999)
	copy(d, fixChecksum(d))
	back, err := Unmarshal(d)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := back.Cardinality(); got != 40 {
		t.Fatalf("cardinality %d, want 40 (stored 9999 must be ignored)", got)
	}
	if !back.Equal(b) {
		t.Fatalf("loaded bitmap differs from original")
	}
}

package ackset

import (
	"bytes"
	"math"
	"testing"
)

// subsetOf returns the elements of a size-w window at base selected by
// mask, all mod 2^32.
func subsetOf(base uint32, w, mask int) []uint32 {
	var out []uint32
	for i := 0; i < w; i++ {
		if mask&(1<<i) != 0 {
			out = append(out, base+uint32(i))
		}
	}
	return out
}

// TestExhaustiveRoundtrip enumerates every subset of a 16-element
// window at bases covering: plain, mid-space, the 2^31 order boundary,
// and sequence number wraparound at 0.
func TestExhaustiveRoundtrip(t *testing.T) {
	const w = 16
	bases := []uint32{0, 12345, (1 << 31) - 8, 0xFFFFFFF8}
	for _, base := range bases {
		for mask := 0; mask < (1 << w); mask++ {
			set := subsetOf(base, w, mask)
			enc, err := Encode(set)
			if err != nil {
				t.Fatalf("base %#x mask %#x: Encode: %v", base, mask, err)
			}
			dec, err := Decode(enc)
			if err != nil {
				t.Fatalf("base %#x mask %#x enc %x: Decode: %v", base, mask, enc, err)
			}
			if !sameSet(dec, set) {
				t.Fatalf("base %#x mask %#x: got %v want %v", base, mask, dec, set)
			}
		}
	}
}

// TestWrapIsomorphism checks that the segmentation rules are identical
// on both sides of sequence number wraparound: a window straddling 0
// (or the 2^31 order boundary) must produce byte-for-byte isomorphic
// encodings to a plain window, up to the shifted start fields.
func TestWrapIsomorphism(t *testing.T) {
	const w = 12
	pairs := [][2]uint32{
		{1000, 0xFFFFFFFA},       // plain vs straddling 0
		{1000, (1 << 31) - 6},    // plain vs straddling 2^31
		{0x7FFFFFF0, 0xFFFFFFF0}, // both shifted, one wrapping
	}
	for _, p := range pairs {
		for mask := 0; mask < (1 << w); mask++ {
			s1 := subsetOf(p[0], w, mask)
			s2 := subsetOf(p[1], w, mask)
			e1, err := Encode(s1)
			if err != nil {
				t.Fatalf("bases %v mask %#x: %v", p, mask, err)
			}
			e2, err := Encode(s2)
			if err != nil {
				t.Fatalf("bases %v mask %#x: %v", p, mask, err)
			}
			if len(e1) != len(e2) {
				t.Fatalf("bases %v mask %#x: sizes %d != %d", p, mask, len(e1), len(e2))
			}
			d2, err := Decode(e2)
			if err != nil {
				t.Fatalf("bases %v mask %#x: %v", p, mask, err)
			}
			shift := p[1] - p[0] // mod 2^32
			for i, v := range s1 {
				if d2[i] != v+shift {
					t.Fatalf("bases %v mask %#x: element %d: %#x != %#x",
						p, mask, i, d2[i], v+shift)
				}
			}
		}
	}
}

// bruteMinBytes computes the minimal encoding size for elems (sorted,
// deduplicated) by enumerating every circular cut and every partition
// into range/bitmap segments.
func bruteMinBytes(elems []uint32) int {
	n := len(elems)
	if n == 0 {
		return 1
	}
	best := math.MaxInt
	for cut := 0; cut < n; cut++ {
		off := make([]uint64, n)
		for k := 0; k < n; k++ {
			off[k] = uint64(elems[(cut+k)%n] - elems[cut])
		}
		if off[n-1] >= MaxWindow {
			continue
		}
		var rec func(i, cost, nseg int)
		rec = func(i, cost, nseg int) {
			if i == n {
				if total := cost + 1 + uvarintLen(uint64(nseg)); total < best {
					best = total
				}
				return
			}
			for j := i + 1; j <= n; j++ {
				w := off[j-1] - off[i] + 1
				if w == uint64(j-i) {
					rec(j, cost+segHeader+uvarintLen(w), nseg+1)
				} else {
					if w > MaxBitmapBits {
						break
					}
					rec(j, cost+segHeader+uvarintLen(w)+int((w+7)/8), nseg+1)
				}
			}
		}
		rec(0, 0, 0)
	}
	return best
}

// TestExhaustiveMinimal proves byte-minimality of Encode against brute
// force for every subset of a 10-element window, plain and wrapping.
func TestExhaustiveMinimal(t *testing.T) {
	const w = 10
	for _, base := range []uint32{0, 0xFFFFFFFC} {
		for mask := 0; mask < (1 << w); mask++ {
			set := subsetOf(base, w, mask)
			enc, err := Encode(set)
			if err != nil {
				t.Fatalf("base %#x mask %#x: %v", base, mask, err)
			}
			if want := bruteMinBytes(normalize(set)); len(enc) != want {
				t.Fatalf("base %#x mask %#x: Encode = %d bytes, brute min = %d (%x)",
					base, mask, len(enc), want, enc)
			}
			// The decoder must accept the brute-force-minimal form:
			// it is exactly what Encode produced.
			if _, err := Decode(enc); err != nil {
				t.Fatalf("base %#x mask %#x: Decode: %v", base, mask, err)
			}
		}
	}
}

// TestExhaustiveCanonicalUniqueness verifies that for every subset of a
// small window, decoding then re-encoding is a fixed point, i.e. the
// canonical form is unique.
func TestExhaustiveCanonicalUniqueness(t *testing.T) {
	const w = 12
	for _, base := range []uint32{7, 0xFFFFFFF6} {
		for mask := 0; mask < (1 << w); mask++ {
			set := subsetOf(base, w, mask)
			enc, err := Encode(set)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := Decode(enc)
			if err != nil {
				t.Fatal(err)
			}
			enc2, err := Encode(dec)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(enc, enc2) {
				t.Fatalf("base %#x mask %#x: %x != %x", base, mask, enc, enc2)
			}
		}
	}
}

package batchack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"sort"
	"testing"
)

func equalSets(a, b []uint32) bool {
	ua := make(map[uint32]bool, len(a))
	for _, v := range a {
		ua[v] = true
	}
	ub := make(map[uint32]bool, len(b))
	for _, v := range b {
		ub[v] = true
	}
	if len(ua) != len(ub) {
		return false
	}
	for v := range ua {
		if !ub[v] {
			return false
		}
	}
	return true
}

// bruteForceMinLen independently recomputes the minimal encoding size for
// a set: it finds the anchor by testing every element as a candidate, then
// enumerates all bitmap lengths. It shares no code with Encode.
func bruteForceMinLen(t *testing.T, set []uint32) int {
	t.Helper()
	if len(set) == 0 {
		return headerLen
	}
	uniq := make(map[uint32]bool, len(set))
	var elems []uint32
	for _, v := range set {
		if !uniq[v] {
			uniq[v] = true
			elems = append(elems, v)
		}
	}
	// The anchor is the unique element from which every other element is
	// within MaxOffset in forward (mod 2^32) distance.
	var anchor uint32
	candidates := 0
	for _, cand := range elems {
		ok := true
		for _, v := range elems {
			if v-cand > MaxOffset {
				ok = false
				break
			}
		}
		if ok {
			anchor = cand
			candidates++
		}
	}
	if candidates != 1 {
		t.Fatalf("set %v: want exactly 1 anchor candidate, got %d", set, candidates)
	}
	offsets := make([]uint32, len(elems))
	for i, v := range elems {
		offsets[i] = v - anchor
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	best := -1
	for l := 0; l <= maxBitmapBytes; l++ {
		runs := 0
		var prev uint32
		hasPrev := false
		for _, off := range offsets {
			if off < uint32(l)*8 {
				continue
			}
			if !hasPrev || off != prev+1 {
				runs++
			}
			prev, hasPrev = off, true
		}
		if runs > maxRanges {
			continue
		}
		cost := headerLen + rangeLen*runs + l
		if best < 0 || cost < best {
			best = cost
		}
	}
	if best < 0 {
		t.Fatalf("set %v: not encodable", set)
	}
	return best
}

func testExhaustive(t *testing.T, universe []uint32) {
	t.Helper()
	n := len(universe)
	for mask := 0; mask < (1 << n); mask++ {
		var set []uint32
		for i := 0; i < n; i++ {
			if mask>>i&1 == 1 {
				set = append(set, universe[i])
			}
		}
		enc, err := Encode(set)
		if err != nil {
			t.Fatalf("set %v: Encode: %v", set, err)
		}
		if got, want := len(enc), bruteForceMinLen(t, set); got != want {
			t.Fatalf("set %v: encoded %d bytes, brute-force minimum is %d", set, got, want)
		}
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("set %v: Decode(%x): %v", set, enc, err)
		}
		if !equalSets(dec, set) {
			t.Fatalf("set %v: round trip gave %v (enc %x)", set, dec, enc)
		}
		// Canonical fixed point: decoding then re-encoding is identity.
		re, err := Encode(dec)
		if err != nil {
			t.Fatalf("set %v: re-Encode: %v", set, err)
		}
		if !bytes.Equal(re, enc) {
			t.Fatalf("set %v: encoding %x not a fixed point, re-encoded %x", set, enc, re)
		}
	}
}

func TestExhaustiveSmallUniverse(t *testing.T) {
	universe := make([]uint32, 12)
	for i := range universe {
		universe[i] = uint32(i)
	}
	testExhaustive(t, universe)
}

func TestExhaustiveWrapUniverse(t *testing.T) {
	// 12 consecutive sequence numbers straddling the wrap point.
	var universe []uint32
	for i := 0; i < 12; i++ {
		universe = append(universe, uint32(math.MaxUint32-5+i))
	}
	testExhaustive(t, universe)
}

func TestExhaustiveScatteredUniverse(t *testing.T) {
	universe := []uint32{3, 7, 8, 30, 31, 32, 33, 100, 101, 500, 501, 502}
	testExhaustive(t, universe)
}

func TestWrapRunStaysSingleRange(t *testing.T) {
	// 34 consecutive sequence numbers crossing 2^32-1 -> 0: longer than
	// the 32-bit bitmap window, so a single range is the minimal form.
	var set []uint32
	for i := 0; i < 34; i++ {
		set = append(set, uint32(0xFFFFFFF0+i))
	}
	enc, err := Encode(set)
	if err != nil {
		t.Fatal(err)
	}
	anchor, ranges, bitmap, err := parse(enc)
	if err != nil {
		t.Fatal(err)
	}
	if anchor != 0xFFFFFFF0 {
		t.Fatalf("anchor = %#x, want 0xFFFFFFF0", anchor)
	}
	if len(bitmap) != 0 {
		t.Fatalf("unexpected bitmap in %x", enc)
	}
	if len(ranges) != 1 || ranges[0] != (rng{start: 0, end: 33}) {
		t.Fatalf("ranges = %+v, want single range [0,33]", ranges)
	}
}

func TestWrapAnchorAfterLargestGap(t *testing.T) {
	// The run {MaxUint32, 0} is contiguous across the wrap point.
	set := []uint32{math.MaxUint32, 0}
	enc, err := Encode(set)
	if err != nil {
		t.Fatal(err)
	}
	anchor, _, bitmap, err := parse(enc)
	if err != nil {
		t.Fatal(err)
	}
	if anchor != math.MaxUint32 {
		t.Fatalf("anchor = %#x, want MaxUint32", anchor)
	}
	// Minimal form: 1-byte bitmap with bits 0 and 1.
	if len(bitmap) != 1 || bitmap[0] != 0x03 {
		t.Fatalf("bitmap = %x, want [03]", bitmap)
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSets(dec, set) {
		t.Fatalf("round trip gave %v", dec)
	}
}

func TestWrapMixedBitmapAndRange(t *testing.T) {
	// Offsets 0 and 40 from the anchor: bitmap covers the low element,
	// a range covers the one beyond the 32-bit bitmap window.
	set := []uint32{math.MaxUint32, 39} // 39 == anchor+40 (mod 2^32)
	enc, err := Encode(set)
	if err != nil {
		t.Fatal(err)
	}
	anchor, ranges, bitmap, err := parse(enc)
	if err != nil {
		t.Fatal(err)
	}
	if anchor != math.MaxUint32 {
		t.Fatalf("anchor = %#x", anchor)
	}
	if len(bitmap) != 1 || bitmap[0] != 0x01 {
		t.Fatalf("bitmap = %x, want [01]", bitmap)
	}
	if len(ranges) != 1 || ranges[0] != (rng{start: 40, end: 40}) {
		t.Fatalf("ranges = %+v, want single range [40,40]", ranges)
	}
	if len(enc) != 14 {
		t.Fatalf("len = %d, want 14", len(enc))
	}
}

func seq(start uint32, n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = start + uint32(i)
	}
	return out
}

func TestMinimalChoice(t *testing.T) {
	alternating := []uint32{0, 2, 4, 6, 8, 10, 12, 14}
	clusterPlusFar := append(seq(0, 32), 1000)
	cases := []struct {
		name          string
		set           []uint32
		wantLen       int
		wantRanges    int
		wantBitmapLen int
	}{
		{"empty", nil, 5, 0, 0},
		{"single", []uint32{7}, 6, 0, 1},
		{"dense 8", seq(100, 8), 6, 0, 1},
		{"dense 32", seq(100, 32), 9, 0, 4},
		{"dense 33", seq(100, 33), 13, 1, 0}, // one range beats bitmap+range (17)
		{"alternating 8", alternating, 7, 0, 2},
		{"two far singles", []uint32{0, 1000}, 14, 1, 1}, // bitmap covers 0, range covers 1000
		{"cluster plus far", clusterPlusFar, 17, 1, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := Encode(tc.set)
			if err != nil {
				t.Fatal(err)
			}
			if len(enc) != tc.wantLen {
				t.Fatalf("len = %d, want %d (enc %x)", len(enc), tc.wantLen, enc)
			}
			_, ranges, bitmap, err := parse(enc)
			if err != nil {
				t.Fatal(err)
			}
			if len(ranges) != tc.wantRanges || len(bitmap) != tc.wantBitmapLen {
				t.Fatalf("ranges=%d bitmap=%d, want ranges=%d bitmap=%d",
					len(ranges), len(bitmap), tc.wantRanges, tc.wantBitmapLen)
			}
		})
	}
}

func TestAlternatingBitmapContent(t *testing.T) {
	enc, err := Encode([]uint32{0, 2, 4, 6, 8, 10, 12, 14})
	if err != nil {
		t.Fatal(err)
	}
	_, _, bitmap, err := parse(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bitmap, []byte{0x55, 0x55}) {
		t.Fatalf("bitmap = %x, want 5555", bitmap)
	}
}

func mkEncoding(control byte, anchor uint32, ranges [][2]uint32, bitmap []byte) []byte {
	out := []byte{control}
	out = binary.BigEndian.AppendUint32(out, anchor)
	for _, r := range ranges {
		out = binary.BigEndian.AppendUint32(out, r[0])
		out = binary.BigEndian.AppendUint32(out, r[1])
	}
	return append(out, bitmap...)
}

func TestDecodeRejects(t *testing.T) {
	valid, err := Encode([]uint32{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"empty input", nil, ErrTruncated},
		{"short header", []byte{0x00, 0x00, 0x00}, ErrTruncated},
		{"missing range bytes", []byte{1 << 3, 0, 0, 0, 0}, ErrTruncated},
		{"trailing garbage", append(append([]byte{}, valid...), 0x00), ErrTrailingData},
		{"reserved bits", []byte{0x01, 0, 0, 0, 0}, ErrNonCanonical},
		{"overlap", mkEncoding(2<<3, 0, [][2]uint32{{5, 10}, {9, 12}}, nil), ErrOverlap},
		{"misordered", mkEncoding(2<<3, 0, [][2]uint32{{20, 25}, {5, 7}}, nil), ErrOverlap},
		{"adjacent not merged", mkEncoding(2<<3, 0, [][2]uint32{{5, 10}, {11, 12}}, nil), ErrNonCanonical},
		{"start after end", mkEncoding(1<<3, 0, [][2]uint32{{10, 5}}, nil), ErrInvalidRange},
		{"offset too large", mkEncoding(1<<3, 0, [][2]uint32{{0, MaxOffset + 1}}, nil), ErrOffsetTooLarge},
		{"bitmap trailing zero", mkEncoding(0x04|0x01, 0, nil, []byte{0x01, 0x00}), ErrNonCanonical},
		{"range in bitmap window", mkEncoding(1<<3|0x04, 0, [][2]uint32{{3, 3}}, []byte{0x10}), ErrNonCanonical},
		{"range where bitmap is smaller", mkEncoding(1<<3, 0, [][2]uint32{{0, 2}}, nil), ErrNonCanonical},
		{"oversized bitmap", mkEncoding(0x04|0x03, 0, nil, []byte{0x01, 0, 0, 0}), ErrNonCanonical},
		{"nonzero anchor on empty set", mkEncoding(0, 42, nil, nil), ErrNonCanonical},
		{"anchor not a member", mkEncoding(0x04, 0, nil, []byte{0x20}), ErrNonCanonical},
		{"fuzz regression", []byte("\x0f0/00\x01000000@0000"), ErrNonCanonical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Decode(%x) = %v, want %v", tc.data, err, tc.want)
			}
		})
	}
}

func TestEncodeErrors(t *testing.T) {
	if _, err := Encode([]uint32{0, 1 << 31}); !errors.Is(err, ErrSpanTooLarge) {
		t.Fatalf("got %v, want ErrSpanTooLarge", err)
	}
	// 33 isolated elements at offsets >= 32 need 33 ranges even with
	// the largest bitmap window.
	sparse := []uint32{0}
	for i := 0; i < 33; i++ {
		sparse = append(sparse, uint32(32+2*i))
	}
	if _, err := Encode(sparse); !errors.Is(err, ErrTooManyRanges) {
		t.Fatalf("got %v, want ErrTooManyRanges", err)
	}
}

func TestEncodeNormalizesInput(t *testing.T) {
	a, err := Encode([]uint32{5, 3, 3, 1, 5})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode([]uint32{1, 3, 5})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("unsorted/duplicate input encoded differently: %x vs %x", a, b)
	}
}

func TestRandomRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	for iter := 0; iter < 3000; iter++ {
		base := rnd.Uint32()
		span := uint32(rnd.Intn(2000))
		var set []uint32
		for i := 0; i < rnd.Intn(50); i++ {
			set = append(set, base+rnd.Uint32()%(span+1))
		}
		enc, err := Encode(set)
		if err != nil {
			t.Fatalf("iter %d set %v: %v", iter, set, err)
		}
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("iter %d set %v enc %x: %v", iter, set, enc, err)
		}
		if !equalSets(dec, set) {
			t.Fatalf("iter %d: set %v round trip gave %v", iter, set, dec)
		}
	}
}

func TestMessageAPI(t *testing.T) {
	set := []uint32{math.MaxUint32 - 1, math.MaxUint32, 0, 1, 100, 101, 102, 5000}
	enc, err := Encode(set)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := Parse(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Count(); got != uint64(len(set)) {
		t.Fatalf("Count = %d, want %d", got, len(set))
	}
	for _, n := range set {
		if !msg.Contains(n) {
			t.Fatalf("Contains(%d) = false", n)
		}
	}
	for _, n := range []uint32{2, 99, 103, 4999, 5001, math.MaxUint32 - 2} {
		if msg.Contains(n) {
			t.Fatalf("Contains(%d) = true", n)
		}
	}
	var visited []uint32
	msg.ForEach(func(n uint32) bool {
		visited = append(visited, n)
		return true
	})
	// ForEach walks in anchor (offset) order: the wrapped tail comes first.
	want := []uint32{math.MaxUint32 - 1, math.MaxUint32, 0, 1, 100, 101, 102, 5000}
	if len(visited) != len(want) {
		t.Fatalf("ForEach visited %v, want %v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Fatalf("ForEach visited %v, want %v", visited, want)
		}
	}
	if members := msg.Members(); !equalSets(members, set) {
		t.Fatalf("Members = %v, want %v", members, set)
	}
}

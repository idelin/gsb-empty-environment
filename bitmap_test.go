package bitmap

import (
	"math/rand"
	"sort"
	"testing"
)

// checkCanonical verifies the internal invariants of the canonical
// representation, including the per-container cardinality cache.
func checkCanonical(t *testing.T, b *Bitmap) {
	t.Helper()
	if len(b.keys) != len(b.cons) {
		t.Fatalf("keys/cons length mismatch: %d vs %d", len(b.keys), len(b.cons))
	}
	prev := -1
	total := 0
	for i, key := range b.keys {
		if int(key) <= prev {
			t.Fatalf("keys not strictly increasing at %d", i)
		}
		prev = int(key)
		c := &b.cons[i]
		if c.card <= 0 {
			t.Fatalf("empty container at key %d", key)
		}
		switch c.kind {
		case kindArray:
			if c.words != nil {
				t.Fatalf("array container %d carries bitmap words", key)
			}
			if len(c.array) != c.card {
				t.Fatalf("container %d: cached card %d != array len %d", key, c.card, len(c.array))
			}
			if c.card > b.threshold {
				t.Fatalf("container %d: array card %d above threshold %d", key, c.card, b.threshold)
			}
			for j := 1; j < len(c.array); j++ {
				if c.array[j] <= c.array[j-1] {
					t.Fatalf("container %d: array not strictly increasing", key)
				}
			}
		case kindBitmap:
			if c.array != nil {
				t.Fatalf("bitmap container %d carries array", key)
			}
			if len(c.words) != bitmapWords {
				t.Fatalf("container %d: words len %d", key, len(c.words))
			}
			if got := popcount(c.words); got != c.card {
				t.Fatalf("container %d: stale cardinality cache %d != %d", key, c.card, got)
			}
			if c.card <= b.threshold {
				t.Fatalf("container %d: bitmap card %d not above threshold %d", key, c.card, b.threshold)
			}
		default:
			t.Fatalf("container %d: unknown kind %d", key, c.kind)
		}
		total += c.card
	}
	if total != b.Cardinality() {
		t.Fatalf("total cardinality %d != cached sum %d", total, b.Cardinality())
	}
}

func sortedKeys(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalUint32s(a, b []uint32) bool {
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

// checkAgainstModel verifies cardinality, exact contents, canonical
// form, and serialization round-trip against the model set.
func checkAgainstModel(t *testing.T, b *Bitmap, m map[uint32]bool) {
	t.Helper()
	if got := b.Cardinality(); got != len(m) {
		t.Fatalf("cardinality %d, model %d", got, len(m))
	}
	if got := b.ToArray(); !equalUint32s(got, sortedKeys(m)) {
		t.Fatalf("contents mismatch with model (got %d values)", len(got))
	}
	for v := range m {
		if !b.Contains(v) {
			t.Fatalf("Contains(%d) = false", v)
		}
	}
	checkCanonical(t, b)
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
	if got := back.Cardinality(); got != len(m) {
		t.Fatalf("round-trip cardinality %d, model %d", got, len(m))
	}
}

func modelIntersect(a, b map[uint32]bool) map[uint32]bool {
	out := make(map[uint32]bool)
	for v := range a {
		if b[v] {
			out[v] = true
		}
	}
	return out
}

func modelUnion(a, b map[uint32]bool) map[uint32]bool {
	out := make(map[uint32]bool, len(a)+len(b))
	for v := range a {
		out[v] = true
	}
	for v := range b {
		out[v] = true
	}
	return out
}

func modelDiff(a, b map[uint32]bool) map[uint32]bool {
	out := make(map[uint32]bool)
	for v := range a {
		if !b[v] {
			out[v] = true
		}
	}
	return out
}

// TestModelRandomized drives random operations against a plain
// map-based set model. Small random thresholds force containers to
// cross the array/bitmap boundary in both directions constantly.
func TestModelRandomized(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	randValue := func() uint32 {
		// Cluster values into a few high keys so containers get dense.
		return uint32(rnd.Intn(6))<<16 | uint32(rnd.Intn(1<<16))
	}
	for trial := 0; trial < 25; trial++ {
		threshold := 1 + rnd.Intn(48)
		a, b := New(threshold), New(threshold)
		ma, mb := make(map[uint32]bool), make(map[uint32]bool)
		for step := 0; step < 250; step++ {
			switch rnd.Intn(12) {
			case 0, 1, 2:
				v := randValue()
				a.Add(v)
				ma[v] = true
			case 3:
				v := randValue()
				a.Remove(v)
				delete(ma, v)
			case 4, 5: // range flip, sometimes spanning containers
				lo := uint64(rnd.Intn(4 * 65536))
				ln := uint64(rnd.Intn(3000))
				if rnd.Intn(10) == 0 {
					ln += 65536 // full-container coverage
				}
				hi := lo + ln
				a.FlipRange(lo, hi)
				for v := lo; v < hi; v++ {
					x := uint32(v)
					if ma[x] {
						delete(ma, x)
					} else {
						ma[x] = true
					}
				}
			case 6, 7:
				v := randValue()
				b.Add(v)
				mb[v] = true
			case 8:
				v := randValue()
				b.Remove(v)
				delete(mb, v)
			case 9:
				a = a.And(b)
				ma = modelIntersect(ma, mb)
			case 10:
				a = a.Or(b)
				ma = modelUnion(ma, mb)
			case 11:
				a = a.AndNot(b)
				ma = modelDiff(ma, mb)
			}
			if step%25 == 0 {
				checkAgainstModel(t, a, ma)
			}
		}
		checkAgainstModel(t, a, ma)
		checkAgainstModel(t, b, mb)
	}
}

// TestThresholdCrossing verifies representation switches in both
// directions via Add/Remove and FlipRange.
func TestThresholdCrossing(t *testing.T) {
	b := New(4)
	for i := uint32(0); i < 5; i++ {
		b.Add(i)
	}
	if b.cons[0].kind != kindBitmap {
		t.Fatalf("card 5 > threshold 4: want bitmap, got %v", b.cons[0].kind)
	}
	b.Remove(0) // card 4 <= threshold
	if b.cons[0].kind != kindArray {
		t.Fatalf("card 4 <= threshold 4: want array, got %v", b.cons[0].kind)
	}

	c := New(4)
	c.FlipRange(10, 20) // 10 values from nothing
	if c.cons[0].kind != kindBitmap {
		t.Fatalf("flip to 10: want bitmap, got %v", c.cons[0].kind)
	}
	c.FlipRange(10, 16) // back down to 4
	if c.cons[0].kind != kindArray {
		t.Fatalf("flip to 4: want array, got %v", c.cons[0].kind)
	}
	want := []uint32{16, 17, 18, 19}
	if got := c.ToArray(); !equalUint32s(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Flip everything away: container must disappear.
	c.FlipRange(16, 20)
	if !c.IsEmpty() {
		t.Fatalf("expected empty bitmap, got %v", c.ToArray())
	}
}

// TestFlipRangeEdges covers boundary conditions of range flips.
func TestFlipRangeEdges(t *testing.T) {
	b := New(8)
	b.FlipRange(0, 3*65536) // three aligned full containers
	if got := b.Cardinality(); got != 3*65536 {
		t.Fatalf("aligned flip cardinality %d", got)
	}
	checkCanonical(t, b)
	b.FlipRange(0, 3*65536) // toggle back
	if !b.IsEmpty() {
		t.Fatalf("double aligned flip not empty")
	}
	// hi == 1<<32 must include math.MaxUint32.
	b.FlipRange((1<<32)-5, 1<<32)
	if got := b.Cardinality(); got != 5 {
		t.Fatalf("top-of-space flip cardinality %d", got)
	}
	if !b.Contains(1<<32 - 1) {
		t.Fatalf("max uint32 not set")
	}
	checkCanonical(t, b)
	b.FlipRange((1<<32)-5, 1<<32+100) // hi clamped to the space: clears the 5
	if !b.IsEmpty() {
		t.Fatalf("clamped flip not empty")
	}
	checkCanonical(t, b)
	b.FlipRange(65535, 65537) // straddles two containers
	if got := b.ToArray(); !equalUint32s(got, []uint32{65535, 65536}) {
		t.Fatalf("got %v", got)
	}
	b.FlipRange(5, 5) // empty range: no-op
	if got := b.Cardinality(); got != 2 {
		t.Fatalf("empty flip changed cardinality to %d", got)
	}
}

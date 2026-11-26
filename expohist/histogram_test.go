package expohist

import (
	"math"
	"math/rand"
	"testing"
)

// naiveHistogram is the independent reference implementation: every
// observation is kept and classified on demand with the textbook formula.
type naiveHistogram struct {
	scale         int
	zeroThreshold float64
	values        []float64
}

func newNaive(scale int, zeroThreshold float64) *naiveHistogram {
	return &naiveHistogram{scale: scale, zeroThreshold: zeroThreshold}
}

func (n *naiveHistogram) observe(v float64) {
	n.values = append(n.values, v)
}

func naiveIndex(v float64, scale int) int {
	// Same textbook formula as the component, normalized against bucket
	// bounds so both implementations decide boundary cases identically.
	if scale <= 0 {
		_, exponent := math.Frexp(v)
		idx := exponent - 1
		if scale < 0 {
			idx >>= -scale
		}
		return idx
	}
	idx := int(math.Floor(math.Log2(v) * math.Ldexp(1.0, scale)))
	if v < LowerBound(scale, idx) {
		idx--
	} else if v >= LowerBound(scale, idx+1) {
		idx++
	}
	return idx
}

// bucketsPerSign returns index -> count for positive (sign >= 0) or negative
// observations, plus the zero-bucket count.
func (n *naiveHistogram) bucketsPerSign(negative bool) (map[int]uint64, uint64) {
	counts := map[int]uint64{}
	var zeroCount uint64
	for _, v := range n.values {
		abs := v
		if v < 0 {
			abs = -abs
		}
		if abs <= n.zeroThreshold {
			zeroCount++
			continue
		}
		if (negative && v >= 0) || (!negative && v < 0) {
			continue
		}
		counts[naiveIndex(abs, n.scale)]++
	}
	return counts, zeroCount
}

func (n *naiveHistogram) sum() float64 {
	var s float64
	for _, v := range n.values {
		s += v
	}
	return s
}

// assertAgainstNaive compares every stored positive/negative bucket, the zero
// bucket, the count and the sum against the naive reference.
func assertAgainstNaive(t *testing.T, h *Histogram, n *naiveHistogram) {
	t.Helper()
	if h.Scale() != n.scale {
		t.Fatalf("scale = %d, want %d", h.Scale(), n.scale)
	}

	wantPos, z1 := n.bucketsPerSign(false)
	wantNeg, z2 := n.bucketsPerSign(true)
	assertRange(t, "positive", h.Positive(), wantPos)
	assertRange(t, "negative", h.Negative(), wantNeg)

	if got, want := h.ZeroCount(), z1+z2; got != want {
		t.Fatalf("zero count = %d, want %d", got, want)
	}
	if got, want := h.Count(), uint64(len(n.values)); got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
	if got, want := h.Sum(), n.sum(); math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Fatalf("sum = %v, want %v", got, want)
	}

	var stored uint64
	for _, c := range h.Positive().Counts {
		stored += c
	}
	for _, c := range h.Negative().Counts {
		stored += c
	}
	if stored+h.ZeroCount() != h.Count() {
		t.Fatalf("bucket counts %d + zero %d != total %d", stored, h.ZeroCount(), h.Count())
	}
}

func assertRange(t *testing.T, name string, got BucketRange, want map[int]uint64) {
	t.Helper()
	gotMap := map[int]uint64{}
	for i, c := range got.Counts {
		// The component keeps a contiguous range including zero-count holes;
		// the naive model only stores occupied buckets, so compare counts at
		// occupied indices. Hole/contiguity behavior has dedicated tests.
		if c > 0 {
			gotMap[got.Offset+i] = c
		}
	}
	if len(gotMap) != len(want) {
		t.Fatalf("%s: bucket index set = %v, want %v", name, gotMap, want)
	}
	for idx, count := range want {
		if count == 0 {
			continue
		}
		if gotMap[idx] != count {
			t.Fatalf("%s: bucket %d count = %d, want %d (full: %v)", name, idx, gotMap[idx], count, gotMap)
		}
	}
	// Any occupied naive bucket must lie inside the component's contiguous range.
	for idx := range want {
		if idx < got.Offset || idx >= got.Offset+len(got.Counts) {
			t.Fatalf("%s: occupied bucket %d outside stored range offset=%d len=%d",
				name, idx, got.Offset, len(got.Counts))
		}
	}
}

// assertContiguous checks that stored ranges cover every index from offset to
// offset+len-1 and that decimal bounds of adjacent buckets share an endpoint.
func assertContiguous(t *testing.T, h *Histogram) {
	t.Helper()
	for _, r := range []BucketRange{h.Positive(), h.Negative()} {
		if len(r.Counts) < 2 {
			continue
		}
		for i := 1; i < len(r.Counts); i++ {
			prevIdx := r.Offset + i - 1
			curIdx := r.Offset + i
			if curIdx != prevIdx+1 {
				t.Fatalf("range not contiguous: indices %d then %d", prevIdx, curIdx)
			}
			prevUpper := UpperBound(h.Scale(), prevIdx)
			curLower := LowerBound(h.Scale(), curIdx)
			if prevUpper != curLower {
				t.Fatalf("bounds gap at index %d -> %d: %v != %v", prevIdx, curIdx, prevUpper, curLower)
			}
		}
	}
}

func randomObservations(rng *rand.Rand, n int) []float64 {
	values := make([]float64, n)
	for i := range values {
		// Magnitude across many decades; small offsets avoid landing exactly
		// on a power-of-two boundary so both index implementations agree.
		magnitude := math.Pow(10, -5+rng.Float64()*12) * (1 + rng.Float64())
		if rng.Intn(2) == 0 {
			magnitude = -magnitude
		}
		values[i] = magnitude
	}
	return values
}

func TestRandomObservationsAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	for _, scale := range []int{-4, -1, 0, 1, 3, 7} {
		h, err := New(scale, 0)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ref := newNaive(scale, 0)
		values := randomObservations(rng, 5000)
		for _, v := range values {
			h.Observe(v)
			ref.observe(v)
		}
		assertAgainstNaive(t, h, ref)

		// Every observed value must sit inside the bounds of its own bucket.
		for _, v := range values {
			if v <= 0 {
				continue
			}
			idx := mapToIndex(v, scale)
			lo, hi := LowerBound(scale, idx), UpperBound(scale, idx)
			if v < lo || v >= hi {
				t.Fatalf("value %v not within bucket %d [%v, %v)", v, idx, lo, hi)
			}
		}
	}
}

func TestMergeDownscaleContiguous(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	h1, _ := New(6, 0)
	h2, _ := New(2, 0)
	ref1 := newNaive(6, 0)
	ref2 := newNaive(2, 0)

	// Build the naive expectation by replaying every value at the target
	// scale (the smaller scale, 2).
	mergedRef := newNaive(2, 0)

	for _, v := range randomObservations(rng, 3000) {
		h1.Observe(v)
		ref1.observe(v)
		mergedRef.observe(v)
	}
	for _, v := range randomObservations(rng, 3000) {
		h2.Observe(v)
		ref2.observe(v)
		mergedRef.observe(v)
	}

	assertAgainstNaive(t, h1, ref1)
	assertAgainstNaive(t, h2, ref2)

	if err := h1.Merge(h2); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if h1.Scale() != 2 {
		t.Fatalf("merged scale = %d, want 2", h1.Scale())
	}
	assertAgainstNaive(t, h1, mergedRef)
	assertContiguous(t, h1)

	// Bounds returned to the caller must be contiguous end to end.
	bounds := h1.PositiveBounds()
	for i := 1; i < len(bounds); i++ {
		if bounds[i].Lower != bounds[i-1].Upper {
			t.Fatalf("positive bounds gap at %d: %v != %v", i, bounds[i].Lower, bounds[i-1].Upper)
		}
		if bounds[i].Lower <= 0 || bounds[i].Upper <= bounds[i].Lower {
			t.Fatalf("invalid bucket bounds: %+v", bounds[i])
		}
	}
}

func TestAllZeroObservations(t *testing.T) {
	h, err := New(3, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 100; i++ {
		h.Observe(0)
	}
	if h.Count() != 100 || h.ZeroCount() != 100 {
		t.Fatalf("count=%d zero=%d, want 100/100", h.Count(), h.ZeroCount())
	}
	if len(h.Positive().Counts) != 0 || len(h.Negative().Counts) != 0 {
		t.Fatalf("expected no signed buckets, got %+v / %+v", h.Positive(), h.Negative())
	}
	if h.Sum() != 0 {
		t.Fatalf("sum = %v, want 0", h.Sum())
	}

	other, _ := New(0, 0)
	for i := 0; i < 25; i++ {
		other.Observe(0)
	}
	if err := h.Merge(other); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if h.Count() != 125 || h.ZeroCount() != 125 {
		t.Fatalf("after merge count=%d zero=%d, want 125/125", h.Count(), h.ZeroCount())
	}
	if len(h.Positive().Counts) != 0 || len(h.Negative().Counts) != 0 {
		t.Fatalf("signed buckets appeared after merging zeros")
	}
}

func TestNegativeObservationsEncodedSeparately(t *testing.T) {
	h, _ := New(2, 0)
	values := []float64{-8.3, -4.7, -1.3, 1.3, 4.7, 8.3}
	ref := newNaive(2, 0)
	for _, v := range values {
		h.Observe(v)
		ref.observe(v)
	}
	assertAgainstNaive(t, h, ref)

	neg := h.Negative()
	pos := h.Positive()
	if len(neg.Counts) == 0 || len(pos.Counts) == 0 {
		t.Fatalf("expected both signs populated: %+v / %+v", neg, pos)
	}
	// The same absolute value maps to the same index on each side.
	wantIdx := mapToIndex(4.7, 2)
	negCount := neg.Counts[wantIdx-neg.Offset]
	posCount := pos.Counts[wantIdx-pos.Offset]
	if negCount != 1 || posCount != 1 {
		t.Fatalf("shared index %d counts neg=%d pos=%d, want 1/1", wantIdx, negCount, posCount)
	}
}

func TestHoleInBucketRange(t *testing.T) {
	h, _ := New(1, 0)
	// Values chosen so their indices are far apart, leaving holes between.
	lo := 1.1 // index 0 at scale 1
	hi := 10.0
	h.Observe(lo)
	h.Observe(hi)

	pos := h.Positive()
	loIdx, hiIdx := mapToIndex(lo, 1), mapToIndex(hi, 1)
	if pos.Offset != loIdx {
		t.Fatalf("offset = %d, want %d", pos.Offset, loIdx)
	}
	if got := pos.Offset + len(pos.Counts) - 1; got != hiIdx {
		t.Fatalf("last index = %d, want %d", got, hiIdx)
	}

	var holes int
	for i, c := range pos.Counts {
		idx := pos.Offset + i
		if idx == loIdx || idx == hiIdx {
			if c != 1 {
				t.Fatalf("bucket %d count = %d, want 1", idx, c)
			}
		} else if c != 0 {
			t.Fatalf("expected hole at index %d, found count %d", idx, c)
		} else {
			holes++
		}
	}
	if holes == 0 {
		t.Fatalf("expected at least one hole, counts = %v", pos.Counts)
	}

	// Bounds must still meet exactly across the holes.
	bounds := h.PositiveBounds()
	for i := 1; i < len(bounds); i++ {
		if bounds[i].Lower != bounds[i-1].Upper {
			t.Fatalf("bounds not contiguous across hole: %v != %v", bounds[i].Lower, bounds[i-1].Upper)
		}
	}
}

func TestVeryLargeScaleDifferenceMergesToOneWideBucket(t *testing.T) {
	t.Run("single wide bucket", func(t *testing.T) {
		coarse, _ := New(0, 0)
		fine, _ := New(12, 0) // scale gap of 12, more than ten

		// Every value stays inside one power-of-two octave [1, 2), so after
		// the 12-bit right shift all fine indices land in coarse index 0.
		for v := 1.01; v < 1.99; v *= 1.01 {
			fine.Observe(v)
		}
		fineTotal := fine.Count()

		if err := coarse.Merge(fine); err != nil {
			t.Fatalf("Merge: %v", err)
		}
		if coarse.Scale() != 0 {
			t.Fatalf("merged scale = %d, want 0", coarse.Scale())
		}

		pos := coarse.Positive()
		if len(pos.Counts) != 1 || pos.Offset != 0 || pos.Counts[0] != fineTotal {
			t.Fatalf("expected one coarse bucket holding %d, got offset=%d counts=%v",
				fineTotal, pos.Offset, pos.Counts)
		}
		bounds := coarse.PositiveBounds()
		if bounds[0].Lower != 1.0 || bounds[0].Upper != 2.0 {
			t.Fatalf("wide bucket bounds = %+v, want [1, 2)", bounds[0])
		}
	})

	t.Run("few very wide buckets", func(t *testing.T) {
		coarse, _ := New(-2, 0)
		fine, _ := New(12, 0) // scale gap of 14, more than ten

		var values []float64
		for v := 0.05; v < 40; v *= 1.15 {
			values = append(values, v)
			fine.Observe(v)
		}
		fineTotal := fine.Count()

		if err := coarse.Merge(fine); err != nil {
			t.Fatalf("Merge: %v", err)
		}

		pos := coarse.Positive()
		if len(pos.Counts) == 0 {
			t.Fatalf("expected coarse buckets after merge")
		}
		var bucketSum uint64
		for _, c := range pos.Counts {
			bucketSum += c
		}
		if bucketSum+coarse.ZeroCount() != fineTotal {
			t.Fatalf("lost observations: buckets %d + zero %d != %d", bucketSum, coarse.ZeroCount(), fineTotal)
		}

		// The 14-bit shift collapses ~100 fine buckets into a handful of
		// coarse buckets; each spans a factor of 2^16 = 65536 in value.
		if len(pos.Counts) > 4 {
			t.Fatalf("expected at most 4 coarse buckets, got %d: %v", len(pos.Counts), pos.Counts)
		}
		bounds := coarse.PositiveBounds()
		for i := 1; i < len(bounds); i++ {
			if bounds[i].Lower != bounds[i-1].Upper {
				t.Fatalf("coarse bounds gap: %v != %v", bounds[i].Lower, bounds[i-1].Upper)
			}
		}
		for _, b := range bounds {
			// At scale -2 every bucket covers a factor of 2^(2^2) = 16;
			// the fine buckets were folded by the 14-bit shift, not widened.
			if b.Upper/b.Lower != 16 {
				t.Fatalf("coarse bucket too narrow: %+v", b)
			}
		}

		first, last := bounds[0], bounds[len(bounds)-1]
		for _, v := range values {
			if v < first.Lower || v >= last.Upper {
				t.Fatalf("value %v outside merged interval [%v, %v)", v, first.Lower, last.Upper)
			}
		}
	})
}

func TestZeroThresholdReclassifiesOnMerge(t *testing.T) {
	fine, _ := New(4, 0)
	coarse, _ := New(0, 0.5)

	fine.Observe(0.2) // fine bucket exists but 0.2 <= merged threshold 0.5
	fine.Observe(3.0)
	coarse.Observe(8.0)

	if err := coarse.Merge(fine); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if coarse.ZeroThreshold() != 0.5 {
		t.Fatalf("zero threshold = %v, want 0.5", coarse.ZeroThreshold())
	}
	if coarse.ZeroCount() != 1 {
		t.Fatalf("zero count = %d, want 1", coarse.ZeroCount())
	}

	// The reclassified observation must not survive in any bucket.
	for _, r := range []BucketRange{coarse.Positive(), coarse.Negative()} {
		for i, c := range r.Counts {
			if c > 0 && UpperBound(coarse.Scale(), r.Offset+i) <= 0.5 {
				t.Fatalf("bucket below zero threshold retained: index %d count %d", r.Offset+i, c)
			}
		}
	}
	if coarse.Count() != 3 {
		t.Fatalf("count = %d, want 3", coarse.Count())
	}
}

func TestConstructionValidation(t *testing.T) {
	if _, err := New(MinScale-1, 0); err != errScaleRange {
		t.Fatalf("expected errScaleRange below min, got %v", err)
	}
	if _, err := New(MaxScale+1, 0); err != errScaleRange {
		t.Fatalf("expected errScaleRange above max, got %v", err)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), -1} {
		if _, err := New(0, bad); err != errThreshold {
			t.Fatalf("threshold %v: expected errThreshold, got %v", bad, err)
		}
	}
	var nilH *Histogram
	if err := nilH.Merge(&Histogram{}); err != errNilReceiver {
		t.Fatalf("nil receiver: got %v, want errNilReceiver", err)
	}
	h, _ := New(0, 0)
	if err := h.Merge(nil); err != errNilArgument {
		t.Fatalf("nil argument: got %v, want errNilArgument", err)
	}
}

func TestBoundRoundTrip(t *testing.T) {
	for _, scale := range []int{-10, -3, 0, 2, 9, 21} {
		for _, idx := range []int{-50, -1, 0, 1, 50} {
			lo := LowerBound(scale, idx)
			hi := UpperBound(scale, idx)
			if lo == 0 || hi == 0 || math.IsInf(lo, 0) || math.IsInf(hi, 0) {
				continue // float underflow/overflow at extreme scales and indices
			}
			if !(lo > 0 && hi > lo) {
				t.Fatalf("scale %d idx %d: invalid bounds [%v, %v)", scale, idx, lo, hi)
			}
			if UpperBound(scale, idx) != LowerBound(scale, idx+1) {
				t.Fatalf("scale %d idx %d: adjacent bounds do not meet", scale, idx)
			}
			// A value close to the lower bound maps back to the same index.
			probe := lo * (1 + 1e-9)
			if probe >= hi {
				probe = math.Sqrt(lo * hi)
			}
			if got := mapToIndex(probe, scale); got != idx {
				t.Fatalf("scale %d idx %d: probe %v mapped to %d", scale, idx, probe, got)
			}
		}
	}
}

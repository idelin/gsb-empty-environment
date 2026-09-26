package efano

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// randNonDecreasing builds an independent reference array of n random
// values in [0, universe), sorted. universe == 0 means the full uint64
// range (sparse).
func randNonDecreasing(rng *rand.Rand, n int, universe uint64) []uint64 {
	vals := make([]uint64, n)
	for i := range vals {
		if universe == 0 {
			vals[i] = rng.Uint64()
		} else {
			vals[i] = rng.Uint64() % universe
		}
	}
	slices.Sort(vals)
	return vals
}

func refLowerBound(ref []uint64, x uint64) uint64 {
	i, _ := slices.BinarySearch(ref, x)
	return uint64(i)
}

type seqCase struct {
	name string
	vals []uint64
}

func testCases(rng *rand.Rand) []seqCase {
	return []seqCase{
		{"empty", nil},
		{"single-zero", []uint64{0}},
		{"single-max", []uint64{math.MaxUint64}},
		{"all-equal", slices.Repeat([]uint64{42}, 257)},
		{"all-zero", make([]uint64, 100)},
		{"max-lt-count", randNonDecreasing(rng, 500, 7)}, // max < n forces l = 0
		{"dense-dups", randNonDecreasing(rng, 5000, 64)}, // heavy duplicates
		{"medium", randNonDecreasing(rng, 3000, 1<<20)},  // classic regime
		{"sparse-huge", randNonDecreasing(rng, 64, 0)},   // full 64-bit range
		{"powers", []uint64{0, 1, 2, 4, 8, 1 << 40, 1 << 62}},
		{"edge-mix", []uint64{0, 0, 0, 1, 1, 2, math.MaxUint64 - 1, math.MaxUint64, math.MaxUint64}},
	}
}

func TestRoundTripAgainstReferenceArray(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, tc := range testCases(rng) {
		t.Run(tc.name, func(t *testing.T) {
			ef, err := EncodeAuto(tc.vals)
			if err != nil {
				t.Fatalf("EncodeAuto: %v", err)
			}
			if ef.Len() != uint64(len(tc.vals)) {
				t.Fatalf("Len = %d, want %d", ef.Len(), len(tc.vals))
			}
			for i, want := range tc.vals {
				if got := ef.At(uint64(i)); got != want {
					t.Fatalf("At(%d) = %d, want %d", i, got, want)
				}
			}
			if got := ef.Decode(); !slices.Equal(got, tc.vals) {
				t.Fatalf("Decode mismatch with reference array")
			}
			if err := ef.Verify(); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestLowerBoundAgainstReferenceArray(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for _, tc := range testCases(rng) {
		t.Run(tc.name, func(t *testing.T) {
			ef, err := EncodeAuto(tc.vals)
			if err != nil {
				t.Fatalf("EncodeAuto: %v", err)
			}
			queries := []uint64{0, 1, 2, 63, 64, 1 << 32, math.MaxUint64 - 1, math.MaxUint64}
			if len(tc.vals) > 0 {
				m := tc.vals[len(tc.vals)-1]
				queries = append(queries, m)
				if m > 0 {
					queries = append(queries, m-1)
				}
				if m < math.MaxUint64 {
					queries = append(queries, m+1) // just past the last value
				}
				queries = append(queries, tc.vals[0])
			}
			for i := 0; i < 200; i++ {
				queries = append(queries, rng.Uint64())
			}
			for _, q := range queries {
				if got, want := ef.LowerBound(q), refLowerBound(tc.vals, q); got != want {
					t.Fatalf("LowerBound(%d) = %d, reference says %d", q, got, want)
				}
			}
		})
	}
}

func TestEqualRangeExactRanks(t *testing.T) {
	ref := []uint64{1, 1, 1, 2, 2, 5, 5, 5, 5, 9}
	ef, err := EncodeAuto(ref)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		x          uint64
		start, end uint64
	}{
		{1, 0, 3}, {2, 3, 5}, {5, 5, 9}, {9, 9, 10},
		{0, 0, 0}, {3, 5, 5}, {4, 5, 5}, {6, 9, 9}, {10, 10, 10},
		{math.MaxUint64, 10, 10},
	}
	for _, c := range cases {
		start, end := ef.EqualRange(c.x)
		if start != c.start || end != c.end {
			t.Errorf("EqualRange(%d) = (%d,%d), want (%d,%d)", c.x, start, end, c.start, c.end)
		}
		if got := ef.Count(c.x); got != c.end-c.start {
			t.Errorf("Count(%d) = %d, want %d", c.x, got, c.end-c.start)
		}
	}
}

func TestEmptySet(t *testing.T) {
	ef, err := EncodeAuto(nil)
	if err != nil {
		t.Fatal(err)
	}
	if ef.Len() != 0 {
		t.Fatalf("Len = %d", ef.Len())
	}
	for _, q := range []uint64{0, 1, math.MaxUint64} {
		if got := ef.LowerBound(q); got != 0 {
			t.Fatalf("LowerBound(%d) on empty = %d, want 0", q, got)
		}
	}
	if s, e := ef.EqualRange(7); s != 0 || e != 0 {
		t.Fatalf("EqualRange on empty = (%d,%d)", s, e)
	}
	// Round-trips through the serialized form too.
	back, err := Parse(ef.MarshalBinary())
	if err != nil {
		t.Fatalf("Parse(empty): %v", err)
	}
	if back.Len() != 0 {
		t.Fatalf("parsed Len = %d", back.Len())
	}
	if err := back.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestAllEqualNoDivisionByZero(t *testing.T) {
	// Small constants: universe <= n, so l must be 0 (no low bits at
	// all) and ChooseL must not divide by zero.
	for _, v := range []uint64{0, 1, 42} {
		ref := slices.Repeat([]uint64{v}, 1000)
		ef, err := EncodeAuto(ref)
		if err != nil {
			t.Fatal(err)
		}
		if ef.LowBits() != 0 {
			t.Fatalf("all-equal(%d): l = %d, want 0", v, ef.LowBits())
		}
		if got := ef.Count(v); got != 1000 {
			t.Fatalf("Count(%d) = %d, want 1000", v, got)
		}
		if got := ef.LowerBound(v + 1); got != 1000 {
			t.Fatalf("LowerBound(%d) = %d, want 1000", v+1, got)
		}
	}
	// All-equal at MaxUint64: l = 0 would need 2^64 unary zeros, so the
	// encoder must instead pick a large l and stay tiny and correct.
	ref := slices.Repeat([]uint64{math.MaxUint64}, 1000)
	ef, err := EncodeAuto(ref)
	if err != nil {
		t.Fatal(err)
	}
	if st := ef.Stats(); st.IndexBytes > 1<<20 {
		t.Fatalf("all-equal MaxUint64: IndexBytes = %d, want small", st.IndexBytes)
	}
	if got := ef.Count(math.MaxUint64); got != 1000 {
		t.Fatalf("Count(MaxUint64) = %d, want 1000", got)
	}
	if got := ef.LowerBound(math.MaxUint64); got != 0 {
		t.Fatalf("LowerBound(MaxUint64) = %d, want 0", got)
	}
}

func TestHugeUniverseNoCatastrophicAllocation(t *testing.T) {
	// A handful of values with an enormous upper bound: the index must
	// stay tiny because l absorbs the universe into the low bits.
	vals := []uint64{0, 1 << 40, 1 << 62, math.MaxUint64}
	ef, err := EncodeAuto(vals)
	if err != nil {
		t.Fatal(err)
	}
	st := ef.Stats()
	if st.IndexBytes > 256 {
		t.Fatalf("IndexBytes = %d, want <= 256 for 4 huge values", st.IndexBytes)
	}
	if got := ef.LowerBound(math.MaxUint64); got != 3 {
		t.Fatalf("LowerBound(MaxUint64) = %d, want 3", got)
	}
	if got := ef.LowerBound(1<<62 + 1); got != 3 {
		t.Fatalf("LowerBound(2^62+1) = %d, want 3", got)
	}
	// A hostile header claiming gigantic bit lengths must be rejected
	// before any allocation sized by it.
	good := ef.MarshalBinary()
	bad := slices.Clone(good)
	for i := 24; i < 32; i++ { // highBits field -> 2^64-1
		bad[i] = 0xff
	}
	// Fix the CRC so only the length check can catch it.
	fixCRC(bad)
	if _, err := Parse(bad); err == nil {
		t.Fatal("Parse accepted absurd bit length")
	}
}

func TestQueryAboveLastReturnsPastEnd(t *testing.T) {
	ref := []uint64{3, 3, 7, 100}
	ef, err := EncodeAuto(ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []uint64{101, 1 << 40, math.MaxUint64} {
		if got := ef.LowerBound(q); got != ef.Len() {
			t.Fatalf("LowerBound(%d) = %d, want past-the-end %d", q, got, ef.Len())
		}
	}
	if got := ef.LowerBound(100); got != 3 {
		t.Fatalf("LowerBound(100) = %d, want 3", got)
	}
}

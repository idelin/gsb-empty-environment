package paircmp

import "math/rand"

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

func mutateBytes(rng *rand.Rand, src []byte, flips int) []byte {
	b := append([]byte(nil), src...)
	for i := 0; i < flips && len(b) > 0; i++ {
		p := rng.Intn(len(b))
		b[p] ^= byte(1 << rng.Intn(8))
	}
	return b
}

// modelCompare is the full-array reference model: it computes maximal
// contiguous difference intervals and the missing tail directly from
// two in-memory byte slices.
func modelCompare(a, b []byte) ([]Interval, *TailMissing) {
	n := min(len(a), len(b))
	var diffs []Interval
	start := -1
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			diffs = append(diffs, Interval{Start: int64(start), End: int64(i)})
			start = -1
		}
	}
	if start >= 0 {
		diffs = append(diffs, Interval{Start: int64(start), End: int64(n)})
	}
	var miss *TailMissing
	if len(a) < len(b) {
		miss = &TailMissing{Side: 0, Start: int64(len(a)), End: int64(len(b))}
	} else if len(a) > len(b) {
		miss = &TailMissing{Side: 1, Start: int64(len(b)), End: int64(len(a))}
	}
	return diffs, miss
}

func intervalsEqual(a, b []Interval) bool {
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

func missingEqual(a, b *TailMissing) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// genPair builds two related arrays with random diffs and random
// length differences.
func genPair(rng *rand.Rand) (a, b []byte) {
	base := randBytes(rng, rng.Intn(200))
	a = append([]byte(nil), base...)
	b = mutateBytes(rng, base, rng.Intn(12))
	switch rng.Intn(3) {
	case 0:
		a = append(a, randBytes(rng, 1+rng.Intn(9))...)
	}
	switch rng.Intn(3) {
	case 0:
		b = append(b, randBytes(rng, 1+rng.Intn(9))...)
	case 1:
		if len(b) > 0 {
			b = b[:rng.Intn(len(b)+1)]
		}
	}
	return a, b
}

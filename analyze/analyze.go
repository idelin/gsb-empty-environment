// Package analyze quantifies how uniformly a hash function distributes
// keys over a consistent hash ring: per-node load statistics, a
// chi-square goodness-of-fit test against the ring's arc proportions,
// a bit-width variance curve, and an avalanche bit-flip analysis.
package analyze

import (
	"encoding/binary"
	"math"
	"math/rand"

	"hashring-analyzer/ring"
)

// KeyGen fills buf with the i-th sample key.
type KeyGen func(buf []byte, i uint64)

// SequentialKeys generates 8-byte big-endian counter keys. All entropy
// is concentrated in the trailing bytes, which is exactly the pattern
// that exposes FNV-1a's missing high-bit avalanche.
func SequentialKeys(buf []byte, i uint64) {
	binary.BigEndian.PutUint64(buf, i)
}

// RandomKeys returns a KeyGen producing uniformly random keys from a
// deterministic source (same seed => same key stream).
func RandomKeys(seed int64) KeyGen {
	rng := rand.New(rand.NewSource(seed))
	return func(buf []byte, i uint64) {
		for j := range buf {
			buf[j] = byte(rng.Uint64())
		}
	}
}

// LoadStats holds the result of mapping a stream of sample keys onto a
// ring and comparing the observed per-node loads with the loads
// expected from the ring's arc proportions.
type LoadStats struct {
	Samples     int
	Counts      []uint64  // observed keys per node
	Expected    []float64 // expected keys per node (arc proportions)
	Mean        float64
	StdDev      float64 // population standard deviation of Counts
	CV          float64 // coefficient of variation, StdDev/Mean
	Min         uint64
	Max         uint64
	MaxMinRatio float64
	ChiSquare   float64
	DF          int
	PValue      float64 // chi-square survival probability
}

// ReducedChiSquare returns ChiSquare/DF; ~1 means consistent with a
// uniform hash, larger values quantify the normalized load variance
// caused by the hash itself.
func (s *LoadStats) ReducedChiSquare() float64 {
	if s.DF == 0 {
		return math.NaN()
	}
	return s.ChiSquare / float64(s.DF)
}

// Pass reports whether the uniformity null hypothesis survives at the
// given significance level (e.g. alpha = 0.01).
func (s *LoadStats) Pass(alpha float64) bool { return s.PValue >= alpha }

// Run maps samples keys onto the ring and computes load statistics.
func Run(r *ring.Ring, gen KeyGen, keyLen, samples int) *LoadStats {
	counts := make([]uint64, r.NumNodes())
	buf := make([]byte, keyLen)
	for i := 0; i < samples; i++ {
		gen(buf, uint64(i))
		counts[r.Map(buf)]++
	}
	fracs := r.ArcFractions()
	expected := make([]float64, len(fracs))
	for i, f := range fracs {
		expected[i] = f * float64(samples)
	}
	return NewLoadStats(samples, counts, expected)
}

// NewLoadStats computes descriptive statistics and the chi-square
// goodness-of-fit test for observed counts against expected counts.
func NewLoadStats(samples int, counts []uint64, expected []float64) *LoadStats {
	n := len(counts)
	s := &LoadStats{
		Samples:  samples,
		Counts:   counts,
		Expected: expected,
		DF:       n - 1,
	}
	if n == 0 {
		return s
	}

	var total uint64
	s.Min = counts[0]
	for _, c := range counts {
		total += c
		if c < s.Min {
			s.Min = c
		}
		if c > s.Max {
			s.Max = c
		}
	}
	s.Mean = float64(total) / float64(n)
	var sumSq float64
	for _, c := range counts {
		d := float64(c) - s.Mean
		sumSq += d * d
	}
	s.StdDev = math.Sqrt(sumSq / float64(n))
	if s.Mean > 0 {
		s.CV = s.StdDev / s.Mean
	}
	if s.Min > 0 {
		s.MaxMinRatio = float64(s.Max) / float64(s.Min)
	} else {
		s.MaxMinRatio = math.Inf(1)
	}

	for i, c := range counts {
		e := expected[i]
		if e <= 0 {
			if c > 0 {
				s.ChiSquare = math.Inf(1)
			}
			continue
		}
		d := float64(c) - e
		s.ChiSquare += d * d / e
	}
	s.PValue = ChiSquarePValue(s.DF, s.ChiSquare)
	return s
}

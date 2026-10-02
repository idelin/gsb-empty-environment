package hashring

import (
	"fmt"
	"math"
	"math/bits"
	"math/rand"
)

// KeyGen produces the i-th sample key. rng may be used for randomness;
// the returned slice is consumed before the next call.
type KeyGen func(rng *rand.Rand, i int) []byte

// RandomKeys yields uniformly random 16-byte keys.
func RandomKeys(rng *rand.Rand, _ int) []byte {
	key := make([]byte, 16)
	rng.Read(key)
	return key
}

// SequentialKeys yields human-style sequential keys ("key-00000000", ...),
// a realistic low-entropy workload that stresses a hash's avalanche.
func SequentialKeys(_ *rand.Rand, i int) []byte {
	return []byte(fmt.Sprintf("key-%08d", i))
}

// Report summarizes the load distribution of one hash function on a ring.
type Report struct {
	HashName    string
	Samples     int
	Nodes       int
	VNodes      int
	Mean        float64
	Variance    float64
	StdDev      float64
	CV          float64 // coefficient of variation, stddev/mean
	Min         int
	Max         int
	MaxMinRatio float64
	Chi2        float64 // against the ring's arc-share expectation
	DF          int
	PValue      float64
	Uniform     bool // p-value > 0.05
}

// CountKeys maps samples keys onto the ring with keyHash and returns the
// per-node load counts.
func CountKeys(ring *Ring, keyHash HashFunc, samples int, seed int64, gen KeyGen) []int {
	rng := rand.New(rand.NewSource(seed))
	counts := make([]int, ring.NumNodes())
	for i := 0; i < samples; i++ {
		counts[ring.Locate(keyHash(gen(rng, i)))]++
	}
	return counts
}

// Summarize builds a Report from raw per-node counts. The chi-square
// goodness-of-fit is computed against the ring's arc shares, so it
// measures hash uniformity rather than vnode placement luck.
func Summarize(name string, counts []int, samples, vnodes int, shares []float64) Report {
	expected := make([]float64, len(counts))
	for i, s := range shares {
		expected[i] = s * float64(samples)
	}
	chi2, df := ChiSquareExpected(counts, expected)
	p := ChiSquarePValue(chi2, df)
	mean := float64(samples) / float64(len(counts))
	var sq float64
	min, max := counts[0], counts[0]
	for _, c := range counts {
		d := float64(c) - mean
		sq += d * d
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	variance := sq / float64(len(counts))
	stddev := math.Sqrt(variance)
	ratio := math.Inf(1)
	if min > 0 {
		ratio = float64(max) / float64(min)
	}
	return Report{
		HashName:    name,
		Samples:     samples,
		Nodes:       len(counts),
		VNodes:      vnodes,
		Mean:        mean,
		Variance:    variance,
		StdDev:      stddev,
		CV:          stddev / mean,
		Min:         min,
		Max:         max,
		MaxMinRatio: ratio,
		Chi2:        chi2,
		DF:          df,
		PValue:      p,
		Uniform:     p > 0.05,
	}
}

// Analyze runs the full sampling pipeline for one hash function.
func Analyze(name string, h HashFunc, nodes []string, vnodes, samples int, seed int64, gen KeyGen) Report {
	ring := NewRing(nodes, vnodes, h)
	counts := CountKeys(ring, h, samples, seed, gen)
	return Summarize(name, counts, samples, vnodes, ring.Shares())
}

// AvalancheReport measures the strict avalanche criterion: each output
// bit should flip with probability ~0.5 when one input bit flips.
type AvalancheReport struct {
	BitProb  [32]float64 // flip probability per output bit
	MeanDev  float64     // mean |p-0.5| over output bits
	MaxDev   float64     // max |p-0.5|
	WorstBit int         // output bit with max deviation
	BadBits  int         // count of bits with |p-0.5| > 0.1
}

// Avalanche flips each of the 64 input bits of random 8-byte inputs and
// records how often every output bit changes.
func Avalanche(h HashFunc, trials int, seed int64) AvalancheReport {
	rng := rand.New(rand.NewSource(seed))
	var flips [32]uint64
	var total uint64
	buf := make([]byte, 8)
	for t := 0; t < trials; t++ {
		rng.Read(buf)
		base := h(buf)
		for bit := 0; bit < 64; bit++ {
			buf[bit/8] ^= 1 << (uint(bit) % 8)
			diff := base ^ h(buf)
			buf[bit/8] ^= 1 << (uint(bit) % 8)
			for diff != 0 {
				flips[bits.TrailingZeros32(diff)]++
				diff &= diff - 1
			}
			total++
		}
	}
	var rep AvalancheReport
	for j := 0; j < 32; j++ {
		p := float64(flips[j]) / float64(total)
		rep.BitProb[j] = p
		dev := math.Abs(p - 0.5)
		rep.MeanDev += dev
		if dev > rep.MaxDev {
			rep.MaxDev = dev
			rep.WorstBit = j
		}
		if dev > 0.1 {
			rep.BadBits++
		}
	}
	rep.MeanDev /= 32
	return rep
}

// WidthPoint is one row of the bit-width avalanche-deviation curve.
type WidthPoint struct {
	Width   int
	MeanDev float64 // mean |p-0.5| over output bits [0, Width)
	MaxDev  float64 // worst |p-0.5| in that window
	BadBits int     // bits with |p-0.5| > 0.1 in that window
}

// WidthCurve derives the avalanche quality of the hash's low `width`
// bits for each requested width. A good hash stays near zero deviation
// at every width; a hash with weak low-bit avalanche (FNV-1a) shows a
// pronounced deviation peak at small widths.
func WidthCurve(h HashFunc, trials int, widths []int, seed int64) []WidthPoint {
	av := Avalanche(h, trials, seed)
	out := make([]WidthPoint, 0, len(widths))
	for _, w := range widths {
		wp := WidthPoint{Width: w}
		for j := 0; j < w && j < 32; j++ {
			dev := math.Abs(av.BitProb[j] - 0.5)
			wp.MeanDev += dev
			if dev > wp.MaxDev {
				wp.MaxDev = dev
			}
			if dev > 0.1 {
				wp.BadBits++
			}
		}
		if w > 0 {
			wp.MeanDev /= float64(w)
		}
		out = append(out, wp)
	}
	return out
}

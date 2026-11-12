package taskpkg

import (
	"math"
)

const noiseBlockLen = 256

// splitMix64 gives a small, deterministic, platform-independent random stream.
type noiseSource struct {
	state uint64
}

func newNoiseSource(seed int64) noiseSource {
	return noiseSource{state: uint64(seed)}
}

func (n *noiseSource) nextUint64() uint64 {
	n.state += 0x9e3779b97f4a7c15
	z := n.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (n *noiseSource) uniform() float64 {
	// 53 random bits produce [0, 1).
	return float64(n.nextUint64()>>11) / (1 << 53)
}

func (n *noiseSource) normal() float64 {
	u1 := n.uniform()
	if u1 == 0 {
		u1 = math.SmallestNonzeroFloat64
	}
	u2 := n.uniform()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// makeNoiseBlock produces a zero-mean block whose RMS is exactly targetRMS
// (up to floating-point rounding). Normalization is done over a whole block,
// not over host chunks, so arbitrary buffer splitting cannot change the
// generated random stream.
func (n *noiseSource) makeNoiseBlock(targetRMS float64) []float64 {
	if targetRMS <= 0 || math.IsNaN(targetRMS) || math.IsInf(targetRMS, 0) {
		return make([]float64, noiseBlockLen)
	}
	for {
		block := make([]float64, noiseBlockLen)
		var mean float64
		for i := range block {
			block[i] = n.normal()
			mean += block[i]
		}
		mean /= float64(len(block))
		var energy float64
		for i := range block {
			block[i] -= mean
			energy += block[i] * block[i]
		}
		blockRMS := math.Sqrt(energy / float64(len(block)))
		if blockRMS <= 1e-18 || math.IsNaN(blockRMS) || math.IsInf(blockRMS, 0) {
			continue
		}
		scale := targetRMS / blockRMS
		for i := range block {
			block[i] *= scale
		}
		return block
	}
}

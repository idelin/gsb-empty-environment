package analyze

import (
	"math/rand"

	"hashring-analyzer/hashfn"
)

// Avalanche holds the result of a strict avalanche criterion probe:
// Prob[inBit][outBit] is the probability that flipping input bit inBit
// flips output bit outBit. An ideal hash scores 0.5 everywhere.
type Avalanche struct {
	KeyLen  int
	Samples int
	Prob    [][]float64 // [inBit][outBit], inBit indexes bytes*8
}

// AvalancheTest flips every input bit of random keyLen-byte keys and
// measures how often each of the 64 output bits changes.
func AvalancheTest(h hashfn.Hash64, keyLen, samples int, seed int64) *Avalanche {
	rng := rand.New(rand.NewSource(seed))
	counts := make([][]uint64, keyLen*8)
	for i := range counts {
		counts[i] = make([]uint64, 64)
	}
	key := make([]byte, keyLen)
	for s := 0; s < samples; s++ {
		for j := range key {
			key[j] = byte(rng.Uint64())
		}
		base := h(key)
		for inBit := 0; inBit < keyLen*8; inBit++ {
			key[inBit/8] ^= 1 << uint(inBit%8)
			diff := base ^ h(key)
			key[inBit/8] ^= 1 << uint(inBit%8)
			for outBit := 0; outBit < 64; outBit++ {
				if diff>>uint(outBit)&1 == 1 {
					counts[inBit][outBit]++
				}
			}
		}
	}
	a := &Avalanche{KeyLen: keyLen, Samples: samples, Prob: make([][]float64, keyLen*8)}
	for i := range counts {
		a.Prob[i] = make([]float64, 64)
		for j := range a.Prob[i] {
			a.Prob[i][j] = float64(counts[i][j]) / float64(samples)
		}
	}
	return a
}

// MeanProb averages flip probabilities over one input byte and the
// output bit range [outLo, outHi] (inclusive).
func (a *Avalanche) MeanProb(inByte, outLo, outHi int) float64 {
	var sum float64
	var n int
	for b := inByte * 8; b < inByte*8+8; b++ {
		for o := outLo; o <= outHi; o++ {
			sum += a.Prob[b][o]
			n++
		}
	}
	return sum / float64(n)
}

// DeficientOutputBits returns the output bits whose flip probability
// falls below threshold for every bit flip within input byte inByte.
// For an ideal hash the result is empty.
func (a *Avalanche) DeficientOutputBits(inByte int, threshold float64) []int {
	var bits []int
	for o := 0; o < 64; o++ {
		deficient := true
		for b := inByte * 8; b < inByte*8+8; b++ {
			if a.Prob[b][o] >= threshold {
				deficient = false
				break
			}
		}
		if deficient {
			bits = append(bits, o)
		}
	}
	return bits
}

// MaxDeviation returns the largest |prob - 0.5| over the whole matrix
// together with the input bit and output bit where it occurs.
func (a *Avalanche) MaxDeviation() (inBit, outBit int, dev float64) {
	for i := range a.Prob {
		for o := range a.Prob[i] {
			d := a.Prob[i][o] - 0.5
			if d < 0 {
				d = -d
			}
			if d > dev {
				dev = d
				inBit = i
				outBit = o
			}
		}
	}
	return inBit, outBit, dev
}

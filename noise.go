package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"math"
	mathrand "math/rand/v2"
)

func newRand(seed *int64) (*mathrand.Rand, error) {
	var key [32]byte
	if seed != nil {
		binary.LittleEndian.PutUint64(key[:8], uint64(*seed))
	} else {
		if _, err := rand.Read(key[:]); err != nil {
			return nil, err
		}
	}
	return mathrand.New(mathrand.NewChaCha8(key)), nil
}

// laplaceSample draws from Laplace(0, scale) using inverse-CDF sampling.
func laplaceSample(rng *mathrand.Rand, scale float64) float64 {
	if scale <= 0 {
		return 0
	}
	u := rng.Float64() - 0.5
	if u == 0 {
		return 0
	}
	return -scale * math.Copysign(math.Log(1-2*math.Abs(u)), u)
}

func roundTo(value, base float64) float64 {
	if base <= 1 || math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	return math.Round(value/base) * base
}

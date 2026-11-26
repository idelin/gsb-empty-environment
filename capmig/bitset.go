package capmig

import (
	"math/big"
	"math/bits"
)

func wordsFor(histories uint64) int {
	if histories == 0 {
		return 1
	}
	return int((histories + 63) / 64)
}

func bigFromWords(words []uint64) *big.Int {
	bitsWords := make([]big.Word, len(words))
	for i, w := range words {
		bitsWords[i] = big.Word(w)
	}
	return new(big.Int).SetBits(bitsWords)
}

func onesCount(words []uint64) uint64 {
	var n uint64
	for _, w := range words {
		n += uint64(bits.OnesCount64(w))
	}
	return n
}

// firstSetBits returns up to limit mask values whose bit is set in words.
func firstSetBits(words []uint64, limit int) []uint64 {
	var out []uint64
	for wi, w := range words {
		for w != 0 {
			b := bits.TrailingZeros64(w)
			out = append(out, uint64(wi)*64+uint64(b))
			if len(out) >= limit {
				return out
			}
			w &= w - 1
		}
	}
	return out
}

func andWords(a, b []uint64) []uint64 {
	out := make([]uint64, len(a))
	for i := range a {
		out[i] = a[i] & b[i]
	}
	return out
}

func andNotWords(a, b []uint64) []uint64 {
	out := make([]uint64, len(a))
	for i := range a {
		out[i] = a[i] &^ b[i]
	}
	return out
}

func xorWords(a, b []uint64) []uint64 {
	out := make([]uint64, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

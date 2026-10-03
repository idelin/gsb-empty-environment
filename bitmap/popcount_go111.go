package bitmap

import "math/bits"

func popcount(w uint64) int { return bits.OnesCount64(w) }

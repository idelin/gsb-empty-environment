package flowlb

import (
	"hash/fnv"
	"math"
)

// unitInterval maps (flowID, key) deterministically to a value in (0, 1).
func unitInterval(flowID, key string) float64 {
	h := fnv.New64a()
	h.Write([]byte(flowID))
	h.Write([]byte{0})
	h.Write([]byte(key))
	x := mix64(h.Sum64())
	// Use the top 53 bits to build a float in (0, 1); the 0.5 offset
	// keeps the result away from the exact endpoints 0 and 1.
	return (float64(x>>11) + 0.5) / float64(uint64(1)<<53)
}

// mix64 is a splitmix64-style finalizer. FNV-1a's high bits barely react to
// differences in the trailing input bytes, so the raw sum is avalanched
// before any bits are selected.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// score implements weighted rendezvous (HRW) hashing: a path with weight w
// wins a flow with probability proportional to w. Weight 0 never wins.
func score(flowID string, p Path) float64 {
	if p.Weight <= 0 {
		return math.Inf(-1)
	}
	return -float64(p.Weight) / math.Log(unitInterval(flowID, p.ID))
}

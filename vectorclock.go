package causal

// VectorClock is indexed by node position in the fixed membership set.
type VectorClock []int

func NewVectorClock(n int) VectorClock { return make(VectorClock, n) }

func (vc VectorClock) Copy() VectorClock {
	out := make(VectorClock, len(vc))
	copy(out, vc)
	return out
}

// Order compares two vector clocks.
// Returns -1 if a happens-before b, +1 if b happens-before a,
// 0 if equal, and 2 if concurrent (incomparable).
func Order(a, b VectorClock) int {
	if len(a) != len(b) {
		panic("causal: vector clock length mismatch")
	}
	less, greater := false, false
	for i := range a {
		if a[i] < b[i] {
			less = true
		} else if a[i] > b[i] {
			greater = true
		}
	}
	switch {
	case less && greater:
		return 2
	case less:
		return -1
	case greater:
		return 1
	default:
		return 0
	}
}

// HappensBefore reports whether a strictly happens-before b.
func HappensBefore(a, b VectorClock) bool { return Order(a, b) == -1 }

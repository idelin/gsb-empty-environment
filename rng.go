package desim

// RNG is a deterministic xorshift64 PRNG. Its entire state is a single
// uint64, which makes it trivially checkpointable (unlike math/rand.Rand).
type RNG struct {
	State uint64
}

func newRNG(seed uint64) RNG {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	return RNG{State: seed}
}

func (r *RNG) Next() uint64 {
	x := r.State
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	r.State = x
	return x
}

// Intn returns a deterministic value in [0, n).
func (r *RNG) Intn(n int) int {
	return int(r.Next() % uint64(n))
}

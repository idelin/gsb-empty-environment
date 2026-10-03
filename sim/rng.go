package sim

// RNG is a deterministic xorshift64* pseudo-random number generator.
// The same seed always produces the same stream, which is what makes
// simulations reproducible.
type RNG struct {
	state uint64
}

// NewRNG returns a deterministic RNG. A zero seed is remapped so the
// generator never gets stuck.
func NewRNG(seed uint64) *RNG {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	return &RNG{state: seed}
}

// Next returns the next pseudo-random uint64.
func (r *RNG) Next() uint64 {
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

// Intn returns a pseudo-random int in [0, n).
func (r *RNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.Next() % uint64(n))
}

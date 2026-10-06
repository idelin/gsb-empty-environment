package sim

// RNG is a deterministic splitmix64 generator owned by a single entity.
// Its state is a single uint64, so it snapshots and restores trivially,
// and one entity's draws never affect any other entity's stream.
type RNG struct {
	state uint64
	calls uint64
}

func newRNG(seed uint64) RNG {
	return RNG{state: seed}
}

// Next returns the next value in the stream and records the draw.
func (r *RNG) Next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	r.calls++
	return z
}

// Intn returns a deterministic value in [0, n).
func (r *RNG) Intn(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return int64(r.Next() % uint64(n))
}

// Calls reports how many random values this generator has produced.
func (r *RNG) Calls() uint64 {
	return r.calls
}

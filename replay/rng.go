package replay

// Rng is a deterministic SplitMix64 PRNG. Its entire state is a single
// uint64, which makes it trivial to snapshot and restore during time travel.
type Rng struct {
	state uint64
}

// NewRng returns a Rng seeded with seed.
func NewRng(seed uint64) *Rng {
	return &Rng{state: seed}
}

// Next advances the generator and returns the next uint64.
func (r *Rng) Next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Intn returns a deterministic value in [0, n).
func (r *Rng) Intn(n uint64) uint64 {
	return r.Next() % n
}

// State exposes the raw generator state for snapshotting.
func (r *Rng) State() uint64 {
	return r.state
}

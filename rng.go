package sim

// RNG is a deterministic splitmix64 pseudo-random stream. Each entity owns an
// independent RNG instance, so one entity's draws can never perturb another
// entity's sequence. Calls counts every draw so replays can be audited.
type RNG struct {
	state uint64
	calls uint64
}

// NewRNG returns a stream seeded with seed.
func NewRNG(seed uint64) *RNG {
	return &RNG{state: seed}
}

// Next advances the stream and returns the next uint64.
func (r *RNG) Next() uint64 {
	r.calls++
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Intn returns a deterministic value in [0, n).
func (r *RNG) Intn(n int) int {
	return int(r.Next() % uint64(n))
}

// Calls reports how many draws this stream has produced.
func (r *RNG) Calls() uint64 {
	return r.calls
}

// deriveSeed mixes the world seed with an entity ID (FNV-1a) so each entity
// gets an independent, stable stream regardless of what other entities exist.
func deriveSeed(worldSeed uint64, entityID string) uint64 {
	h := uint64(14695981039346656037)
	mix := func(b byte) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	for i := 0; i < 8; i++ {
		mix(byte(worldSeed >> (8 * uint(i))))
	}
	for i := 0; i < len(entityID); i++ {
		mix(entityID[i])
	}
	return h
}

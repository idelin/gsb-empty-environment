package kernel

// PRNG is a splitmix64 generator. It is used instead of math/rand so that
// the full generator state is a single uint64 that can be snapshotted and
// restored byte-for-byte, which is a hard requirement for deterministic
// time travel.
type PRNG struct {
	state uint64
}

// NewPRNG returns a PRNG seeded with seed. The seed is always taken from
// the event log so replays draw the exact same sequence.
func NewPRNG(seed uint64) PRNG {
	return PRNG{state: seed}
}

// Next advances the generator and returns the next pseudo-random uint64.
func (p *PRNG) Next() uint64 {
	p.state += 0x9E3779B97F4A7C15
	z := p.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// State exposes the raw generator state for snapshotting.
func (p *PRNG) State() uint64 {
	return p.state
}

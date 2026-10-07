package wsample

// Source is an injectable uniform random source. Implementations must
// return integers uniformly distributed over [0, n).
type Source interface {
	// Intn returns a uniform random integer in [0, n). n must be > 0.
	Intn(n int64) int64
}

// Splitmix64 is a small deterministic Source whose entire state is a
// single uint64, which makes it trivial to export and restore.
type Splitmix64 struct {
	state uint64
}

// NewSplitmix64 returns a Splitmix64 source seeded with seed.
func NewSplitmix64(seed uint64) *Splitmix64 {
	return &Splitmix64{state: seed}
}

func (s *Splitmix64) next() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Intn returns a uniform integer in [0, n) using rejection sampling,
// so every value in the interval is equally likely (no modulo bias).
func (s *Splitmix64) Intn(n int64) int64 {
	if n <= 0 {
		panic("wsample: Intn called with non-positive n")
	}
	un := uint64(n)
	// Largest multiple of n that fits in a uint64.
	limit := ^uint64(0) - (^uint64(0) % un)
	for {
		x := s.next()
		if x < limit {
			return int64(x % un)
		}
	}
}

// State exports the full state of the source.
func (s *Splitmix64) State() uint64 {
	return s.state
}

// SetState restores a state previously obtained from State. After
// restoring, the source produces exactly the same sequence of values
// as the original would have.
func (s *Splitmix64) SetState(state uint64) {
	s.state = state
}

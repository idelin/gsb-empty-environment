package contab

import "math/rand"

// randSource is the part of *rand.Rand the shuffler needs, so callers may
// pass their own source (e.g. seeded for reproducibility).
type randSource interface {
	Intn(n int) int
}

// shuffleRecords permutes recs in place with Fisher-Yates. It only reorders:
// the multiset of records, and hence every cell quota, is preserved. A nil
// rng falls back to a fixed seed so results stay reproducible.
func shuffleRecords(recs []Record, rng randSource) {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	for i := len(recs) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		recs[i], recs[j] = recs[j], recs[i]
	}
}

package tablegen

import "math/rand"

// Record is one generated observation tagged by its two category indices.
type Record struct {
	Row, Col int
}

// Generate solves the contingency table for spec, expands it into one
// Record per unit of count, and returns the records in random order.
// Shuffling permutes the slice only; per-cell quotas are exactly those of
// the solved table. A nil rng yields a deterministic (unshuffled) order.
func Generate(spec Spec, rng *rand.Rand) ([]Record, error) {
	t, err := Solve(spec)
	if err != nil {
		return nil, err
	}
	recs := make([]Record, 0, spec.rowSum())
	for i := range t {
		for j := range t[i] {
			for k := 0; k < t[i][j]; k++ {
				recs = append(recs, Record{Row: i, Col: j})
			}
		}
	}
	if rng != nil {
		rng.Shuffle(len(recs), func(a, b int) {
			recs[a], recs[b] = recs[b], recs[a]
		})
	}
	return recs, nil
}

// Counts folds records back into a contingency table with the given
// dimensions, useful for verifying quotas.
func Counts(recs []Record, rows, cols int) Table {
	t := make(Table, rows)
	for i := range t {
		t[i] = make([]int, cols)
	}
	for _, r := range recs {
		t[r.Row][r.Col]++
	}
	return t
}

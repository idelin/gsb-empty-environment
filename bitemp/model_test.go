package bitemp

import (
	"math/rand"
	"testing"
)

// The 2D point-query cross-check: replay identical random operation
// streams against Store (interval splitting) and Model (brute-force
// rectangles), then compare answers over the full grid of
// (valid time) x (known seq) points, including snapshots taken
// mid-stream to exercise backtracking.
func TestStoreMatchesModel(t *testing.T) {
	seeds := []int64{1, 7, 42, 2024, 99991}
	for _, seed := range seeds {
		r := rand.New(rand.NewSource(seed))
		s := NewStore()
		m := NewModel()
		keys := []string{"alpha", "beta", "gamma"}

		const horizon = 24
		checkpoint := 0
		for op := 0; op < 300; op++ {
			key := keys[r.Intn(len(keys))]
			from := int64(r.Intn(horizon))
			to := from + 1 + int64(r.Intn(8))
			var ss, sm uint64
			if r.Intn(4) == 0 {
				ss = s.Delete(key, from, to)
				sm = m.Delete(key, from, to)
			} else {
				val := string('a' + byte(r.Intn(26)))
				ss = s.Put(key, val, from, to)
				sm = m.Put(key, val, from, to)
			}
			if ss != sm {
				t.Fatalf("seed %d op %d: seq mismatch store=%d model=%d", seed, op, ss, sm)
			}
			// Every 60 ops, freeze a full-grid checkpoint at this seq.
			if op%60 == 59 {
				checkGrid(t, seed, s, m, keys, horizon, ss)
				checkpoint++
			}
		}
		// Final grid over every known snapshot, old ones included.
		checkGrid(t, seed, s, m, keys, horizon, s.Seq())
		if checkpoint == 0 {
			t.Fatal("no checkpoints taken")
		}
	}
}

// checkGrid compares Store and Model on every point (t, asOf) with
// t in [-1, horizon+1] and asOf in [0, maxSeq].
func checkGrid(t *testing.T, seed int64, s *Store, m *Model, keys []string, horizon int64, maxSeq uint64) {
	t.Helper()
	for _, key := range keys {
		for vt := int64(-1); vt <= horizon+1; vt++ {
			for asOf := uint64(0); asOf <= maxSeq; asOf++ {
				gv, gok := s.Get(key, vt, asOf)
				wv, wok := m.Get(key, vt, asOf)
				if gv != wv || gok != wok {
					t.Fatalf("seed %d key %q point (t=%d, asOf=%d): store=(%q,%v) model=(%q,%v)",
						seed, key, vt, asOf, gv, gok, wv, wok)
				}
			}
		}
	}
}

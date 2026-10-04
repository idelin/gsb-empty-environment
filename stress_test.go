package bptree

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// longScan is an in-progress range scan pinned to one snapshot version,
// checked against an immutable reference map taken at snapshot time.
type longScan struct {
	snap *Snapshot
	it   *Iterator
	keys []int64
	vals map[int64][]byte
	pos  int
}

func newLongScan(t *testing.T, tr *Tree, model map[int64][]byte) *longScan {
	snap := tr.Snapshot()
	vals := make(map[int64][]byte, len(model))
	var keys []int64
	for k, v := range model {
		vals[k] = v
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	// Range covers every possible test key: [min, max) with margin.
	return &longScan{snap: snap, it: snap.Iterator(-1, 1<<40), keys: keys, vals: vals}
}

func (s *longScan) step(t *testing.T, n int) bool {
	for i := 0; i < n && s.pos < len(s.keys); i++ {
		if !s.it.Next() {
			t.Fatalf("scan exhausted at %d/%d, err=%v (missing keys)", s.pos, len(s.keys), s.it.Err())
		}
		want := s.keys[s.pos]
		if s.it.Key() != want {
			t.Fatalf("scan key = %d, want %d (duplicate/missing/reordered)", s.it.Key(), want)
		}
		if string(s.it.Value()) != string(s.vals[want]) {
			t.Fatalf("scan value for %d = %q, want pinned %q (old value not stable)",
				want, s.it.Value(), s.vals[want])
		}
		s.pos++
	}
	return s.pos < len(s.keys)
}

func (s *longScan) finish(t *testing.T) {
	for s.step(t, 64) {
	}
	if s.it.Next() {
		t.Fatalf("scan yielded extra key %d (duplicate)", s.it.Key())
	}
	if err := s.it.Err(); err != nil {
		t.Fatalf("scan err: %v", err)
	}
	s.it.Close()
	s.snap.Close()
}

// Randomized reference test: an immutable sorted map is the model. Write
// transactions causing splits and merges are interleaved with long-running
// scans; every scan must observe its pinned version exactly — no duplicates,
// no missing keys, values stable.
func TestReferenceModelRandom(t *testing.T) {
	for _, seed := range []int64{42, 7, 1999} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			runReferenceModel(t, seed)
		})
	}
}

func runReferenceModel(t *testing.T, seed int64) {
	rnd := rand.New(rand.NewSource(seed))
	tr := mustTree(t, 4)
	model := make(map[int64][]byte)
	var scans []*longScan

	for op := 0; op < 4000; op++ {
		switch r := rnd.Intn(100); {
		case r < 38: // put (sometimes in multi-key transactions)
			tx := tr.Begin()
			n := 1 + rnd.Intn(6)
			for i := 0; i < n; i++ {
				k := int64(rnd.Intn(300))
				v := []byte(fmt.Sprintf("v%d-%d", k, rnd.Int63()))
				if err := tx.Put(k, v); err != nil {
					t.Fatalf("op %d Put: %v", op, err)
				}
				model[k] = v
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("op %d Commit: %v", op, err)
			}
		case r < 58: // delete
			tx := tr.Begin()
			n := 1 + rnd.Intn(4)
			for i := 0; i < n; i++ {
				k := int64(rnd.Intn(300))
				if _, ok := model[k]; !ok {
					continue
				}
				if err := tx.Delete(k); err != nil {
					t.Fatalf("op %d Delete(%d): %v", op, k, err)
				}
				delete(model, k)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("op %d Commit: %v", op, err)
			}
		case r < 66: // start a long scan over a snapshot of the model
			if len(scans) < 4 {
				scans = append(scans, newLongScan(t, tr, model))
			}
		case r < 90: // advance all scans a little
			for _, s := range scans {
				s.step(t, 1+rnd.Intn(5))
			}
		case r < 94: // finish the oldest scan
			if len(scans) > 0 {
				scans[0].finish(t)
				scans = scans[1:]
			}
		case r < 97: // structural validation
			if err := tr.Validate(); err != nil {
				t.Fatalf("op %d Validate: %v", op, err)
			}
		default: // reclamation
			if _, err := tr.Collect(); err != nil {
				t.Fatalf("op %d Collect: %v", op, err)
			}
		}

		// Spot-check point reads against the model.
		if op%50 == 0 {
			for _, k := range []int64{int64(rnd.Intn(300)), int64(rnd.Intn(300))} {
				want, ok := model[k]
				got, err := tr.Get(k)
				if !ok {
					if err != ErrNotFound {
						t.Fatalf("op %d Get(%d) err = %v, want ErrNotFound", op, k, err)
					}
				} else if err != nil || string(got) != string(want) {
					t.Fatalf("op %d Get(%d) = %q, %v; want %q", op, k, got, err, want)
				}
			}
		}
	}
	for _, s := range scans {
		s.finish(t)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("final Validate: %v", err)
	}
}

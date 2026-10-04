package bptree

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// refMap is an immutable ordered map used as the reference model. Each
// commit clones it; old versions are never mutated, mirroring the B+ tree's
// snapshot semantics.
type refMap struct {
	m map[int64][]byte
}

func newRefMap() *refMap { return &refMap{m: map[int64][]byte{}} }

func (r *refMap) clone() *refMap {
	out := &refMap{m: make(map[int64][]byte, len(r.m))}
	for k, v := range r.m {
		out.m[k] = v
	}
	return out
}

func (r *refMap) sortedKeys() []int64 {
	keys := make([]int64, 0, len(r.m))
	for k := range r.m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// scanEntry is one expected (key, value) pair of a range scan.
type scanEntry struct {
	k int64
	v []byte
}

func (r *refMap) rangeEntries(start, end int64) []scanEntry {
	var out []scanEntry
	for _, k := range r.sortedKeys() {
		if k >= start && k < end {
			out = append(out, scanEntry{k, r.m[k]})
		}
	}
	return out
}

// TestAgainstImmutableRefModel interleaves random write transactions (which
// constantly split and merge leaves at capacity 4) with long snapshot scans
// that are themselves interleaved with more writes. Every scan is checked
// against the immutable reference version pinned by the snapshot: no
// duplicates, no omissions, and old values must remain stable.
func TestAgainstImmutableRefModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	db := openTemp(t, 4)

	const keySpace = 150
	cur := newRefMap()

	type pinned struct {
		snap *Snapshot
		ref  *refMap
	}
	var open []pinned

	randVal := func() []byte {
		n := rng.Intn(24)
		b := make([]byte, n)
		rng.Read(b)
		return b
	}

	checkScan := func(p pinned, start, end int64, round int) {
		t.Helper()
		want := p.ref.rangeEntries(start, end)
		it := p.snap.Scan(start, end)
		got := 0
		for it.Next() {
			if got >= len(want) {
				t.Fatalf("round %d: scan returned extra key %d (dup or new-version leak)", round, it.Key())
			}
			if it.Key() != want[got].k {
				t.Fatalf("round %d: scan key[%d] = %d, want %d (omission or reorder)", round, got, it.Key(), want[got].k)
			}
			if !bytes.Equal(it.Value(), want[got].v) {
				t.Fatalf("round %d: scan value for key %d changed under snapshot", round, it.Key())
			}
			got++
		}
		if err := it.Err(); err != nil {
			t.Fatalf("round %d: iterator: %v", round, err)
		}
		if got != len(want) {
			t.Fatalf("round %d: scan returned %d keys, want %d (omission)", round, got, len(want))
		}
	}

	const rounds = 400
	for round := 0; round < rounds; round++ {
		// One write transaction with a random batch of puts/deletes.
		next := cur.clone()
		ops := 1 + rng.Intn(12)
		if err := db.Update(func(tx *Tx) error {
			for o := 0; o < ops; o++ {
				k := int64(rng.Intn(keySpace))
				if rng.Intn(100) < 60 {
					v := randVal()
					if err := tx.Put(k, v); err != nil {
						return err
					}
					next.m[k] = v
				} else {
					if err := tx.Delete(k); err != nil {
						return err
					}
					delete(next.m, k)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("round %d: Update: %v", round, err)
		}
		cur = next

		// Validate the committed tree structure.
		if round%3 == 0 {
			if err := db.Validate(db.Root()); err != nil {
				t.Fatalf("round %d: Validate: %v", round, err)
			}
		}

		// Randomly pin snapshots of the current version.
		if rng.Intn(100) < 25 && len(open) < 6 {
			open = append(open, pinned{db.Snapshot(), cur})
		}

		// Long scan of a random pinned snapshot, interleaved with extra
		// write transactions in the middle of the scan.
		if len(open) > 0 && rng.Intn(100) < 40 {
			idx := rng.Intn(len(open))
			p := open[idx]
			start := int64(rng.Intn(keySpace))
			end := start + int64(rng.Intn(keySpace/2))
			want := p.ref.rangeEntries(start, end)
			it := p.snap.Scan(start, end)
			got := 0
			for it.Next() {
				if got < len(want) && (it.Key() != want[got].k || !bytes.Equal(it.Value(), want[got].v)) {
					t.Fatalf("round %d: interleaved scan mismatch at %d", round, got)
				}
				got++
				// Interleave a write transaction mid-scan.
				if got%7 == 3 {
					mid := cur.clone()
					if err := db.Update(func(tx *Tx) error {
						for o := 0; o < 5; o++ {
							k := int64(rng.Intn(keySpace))
							if rng.Intn(2) == 0 {
								v := randVal()
								if err := tx.Put(k, v); err != nil {
									return err
								}
								mid.m[k] = v
							} else {
								if err := tx.Delete(k); err != nil {
									return err
								}
								delete(mid.m, k)
							}
						}
						return nil
					}); err != nil {
						t.Fatalf("round %d: interleaved Update: %v", round, err)
					}
					cur = mid
				}
			}
			if err := it.Err(); err != nil {
				t.Fatalf("round %d: interleaved iterator: %v", round, err)
			}
			if got != len(want) {
				t.Fatalf("round %d: interleaved scan got %d keys, want %d", round, got, len(want))
			}
		}

		// Verify all pinned snapshots fully every so often.
		if round%17 == 0 {
			for _, p := range open {
				checkScan(p, -1<<62, 1<<62, round)
				if err := db.Validate(p.snap.Root()); err != nil {
					t.Fatalf("round %d: Validate pinned root: %v", round, err)
				}
			}
		}

		// Randomly close snapshots and reclaim garbage.
		if len(open) > 0 && rng.Intn(100) < 20 {
			idx := rng.Intn(len(open))
			open[idx].snap.Close()
			open = append(open[:idx], open[idx+1:]...)
		}
		if rng.Intn(100) < 15 {
			if _, err := db.Reclaim(); err != nil {
				t.Fatalf("round %d: Reclaim: %v", round, err)
			}
		}
	}

	// Final: close everything, reclaim, and compare the current version
	// against the reference model exhaustively.
	for _, p := range open {
		checkScan(p, -1<<62, 1<<62, rounds)
		p.snap.Close()
	}
	if _, err := db.Reclaim(); err != nil {
		t.Fatalf("final Reclaim: %v", err)
	}
	final := newRefMap()
	final.m = cur.m
	snap := db.Snapshot()
	defer snap.Close()
	checkScan(pinned{snap, final}, -1<<62, 1<<62, rounds)
	for _, k := range final.sortedKeys() {
		v, err := db.Get(k)
		if err != nil {
			t.Fatalf("final Get(%d): %v", k, err)
		}
		if !bytes.Equal(v, final.m[k]) {
			t.Fatalf("final Get(%d) value mismatch", k)
		}
	}
	if err := db.CheckIntegrity(); err != nil {
		t.Fatalf("final CheckIntegrity: %v", err)
	}
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("final Validate: %v", err)
	}
	t.Logf("reference model matched after %d rounds, %d keys live", rounds, len(final.m))
}

// TestConcurrentReadWrite hammers the DB with one writer and several
// snapshot scanners plus a reclaimer. Run with -race.
func TestConcurrentReadWrite(t *testing.T) {
	db := openTemp(t, 8)
	fill(t, db, 200, "init")

	done := make(chan struct{})
	errCh := make(chan error, 16)

	// Writer: batches of puts and deletes.
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			base := int64(i * 7)
			err := db.Update(func(tx *Tx) error {
				for j := int64(0); j < 6; j++ {
					k := (base + j*13) % 400
					if err := tx.Put(k, val(fmt.Sprintf("w%d-%d", i, j))); err != nil {
						return err
					}
				}
				return tx.Delete(base % 400)
			})
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Readers: scan pinned snapshots, verify order and no duplicates.
	var readersDone [3]chan struct{}
	for r := 0; r < 3; r++ {
		readersDone[r] = make(chan struct{})
		go func() {
			defer close(readersDone[r])
			for {
				select {
				case <-done:
					return
				default:
				}
				snap := db.Snapshot()
				it := snap.Scan(-1<<62, 1<<62)
				prev := int64(-1) << 62
				first := true
				for it.Next() {
					k := it.Key()
					if !first && k <= prev {
						errCh <- fmt.Errorf("reader saw out-of-order/dup key %d after %d", k, prev)
						snap.Close()
						return
					}
					first = false
					prev = k
				}
				if err := it.Err(); err != nil {
					errCh <- err
					snap.Close()
					return
				}
				snap.Close()
			}
		}()
	}

	// Reclaimer.
	reclaimDone := make(chan struct{})
	go func() {
		defer close(reclaimDone)
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := db.Reclaim(); err != nil {
				errCh <- err
				return
			}
		}
	}()

	<-done
	for _, c := range readersDone {
		<-c
	}
	<-reclaimDone
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent error: %v", err)
	}
	if err := db.Validate(db.Root()); err != nil {
		t.Fatalf("Validate after concurrency: %v", err)
	}
	if err := db.CheckIntegrity(); err != nil {
		t.Fatalf("CheckIntegrity after concurrency: %v", err)
	}
}

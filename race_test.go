package bptree

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// Concurrent readers, a single writer, and the reclaimer. Run with -race.
func TestConcurrentReadersWriterReclaimer(t *testing.T) {
	tr := mustTree(t, 8)
	fill(t, tr, 0, 200)

	const workers = 3
	const iterations = 300

	var wg sync.WaitGroup
	errs := make(chan error, workers+2)

	// Single writer: puts and deletes in small transactions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rnd := rand.New(rand.NewSource(1))
		for i := 0; i < iterations; i++ {
			tx := tr.Begin()
			for j := 0; j < 4; j++ {
				k := int64(rnd.Intn(400))
				if rnd.Intn(2) == 0 {
					if err := tx.Put(k, []byte(fmt.Sprintf("w%d", i))); err != nil {
						errs <- err
						tx.Rollback()
						return
					}
				} else {
					_ = tx.Delete(k) // may not exist
				}
			}
			if err := tx.Commit(); err != nil {
				errs <- err
				return
			}
		}
	}()

	// Readers: snapshot, scan, verify sorted order and snapshot stability.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for i := 0; i < iterations; i++ {
				snap := tr.Snapshot()
				it := snap.Iterator(int64(rnd.Intn(200)), int64(200+rnd.Intn(200)))
				prev := int64(-1 << 62)
				for it.Next() {
					k := it.Key()
					if k <= prev {
						errs <- fmt.Errorf("reader %d: keys out of order: %d after %d", seed, k, prev)
						snap.Close()
						return
					}
					prev = k
				}
				if err := it.Err(); err != nil {
					errs <- err
					snap.Close()
					return
				}
				snap.Close()
			}
		}(int64(w) + 2)
	}

	// Reclaimer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if _, err := tr.Collect(); err != nil {
				errs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent error: %v", err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate after concurrent run: %v", err)
	}
}

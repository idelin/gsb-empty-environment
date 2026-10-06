package memtx

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentAccess exercises the store from many goroutines so the
// race detector can validate the locking around snapshots and commits.
func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	seed(t, s, "a", "1")

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				tx := s.Begin()
				key := fmt.Sprintf("w%d-k%d", w, i%5)
				if err := tx.Put(key, fmt.Sprintf("%d", i)); err != nil {
					t.Error(err)
					return
				}
				if _, _, err := tx.Get(key); err != nil {
					t.Error(err)
					return
				}
				if _, _, err := tx.Scan(ScanOptions{Limit: 3}); err != nil {
					t.Error(err)
					return
				}
				// Conflicts are expected and fine here.
				_ = tx.Commit()
			}
		}(w)
	}
	wg.Wait()
}

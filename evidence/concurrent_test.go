package evidence

import (
	"sync"
	"testing"
)

// TestConcurrentAccess exercises the mutex under mixed concurrent workloads so
// the race detector (go test -race) can validate internal state ownership.
func TestConcurrentAccess(t *testing.T) {
	b := NewBuffer(4096)
	var wg sync.WaitGroup

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				base := (worker*200 + i) * 8
				if err := b.Write(int64(base), mkBytes(base, 8)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(w)
	}

	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				event := uint64(worker*100 + i + 1)
				start := int64((worker*100 + i) * 8)
				err := b.Notify(Candidate{
					EventID:       event,
					Match:         Range{Start: start, End: start + 4},
					BeforeContext: 2,
					AfterContext:  2,
				})
				if err != nil {
					t.Errorf("notify: %v", err)
					return
				}
				if _, err := b.Collect(event); err != nil {
					t.Errorf("collect: %v", err)
					return
				}
				if err := b.Cancel(event); err != nil {
					t.Errorf("cancel: %v", err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
}

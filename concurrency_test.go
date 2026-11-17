package netsec

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentReadersShareChainsDuringInstall(t *testing.T) {
	h := NewHost(1, 2, 3, 4)
	if err := h.Install(Filter{Rules: []Rule{DenyRule(1, 13)}}); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				threads := h.Threads()
				if len(threads) == 0 {
					t.Error("thread snapshot is empty")
					return
				}
				shared := threads[0].Chain
				_ = shared.Evaluate(1, [6]uint64{})
				for _, thread := range threads[1:] {
					if thread.Chain != shared {
						t.Errorf("thread %d temporarily saw a different chain", thread.ID)
						return
					}
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		filter := Filter{Rules: []Rule{DenyRule(uint32(i%7+1), uint32(i+1))}}
		if err := h.Install(filter); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
}

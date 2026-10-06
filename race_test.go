package objstore

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestConcurrentBoundaryRaces 用虚拟时钟持续推进 + 并发引用操作重放边界竞争，
// 结束后用引用账核对：所有引用归零、没有对象卡在待删态、代际不回退。
// 建议配合 go test -race 运行。
func TestConcurrentBoundaryRaces(t *testing.T) {
	clk := NewFakeClock(time.Unix(1700000000, 0))
	s := NewStore(clk, 5*time.Millisecond)

	ids := []string{"alpha", "bravo", "charlie", "delta"}
	for _, id := range ids {
		if err := s.Create(id); err != nil {
			t.Fatalf("Create(%q): %v", id, err)
		}
	}

	var mu sync.Mutex
	var held []*Ref

	stop := make(chan struct{})
	var clockWG sync.WaitGroup
	clockWG.Add(1)
	go func() {
		defer clockWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				clk.Advance(time.Millisecond)
			}
		}
	}()

	var workers sync.WaitGroup
	for w := 0; w < 8; w++ {
		workers.Add(1)
		go func(seed int64) {
			defer workers.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				id := ids[rng.Intn(len(ids))]
				switch rng.Intn(4) {
				case 0:
					r, err := s.AddRef(id)
					switch err {
					case nil:
						mu.Lock()
						held = append(held, r)
						mu.Unlock()
					case ErrNotFound, ErrDeleted:
					default:
						t.Errorf("AddRef(%q) = %v", id, err)
					}
				case 1:
					if err := s.Delete(id); err != nil && err != ErrDeleted && err != ErrNotFound {
						t.Errorf("Delete(%q) = %v", id, err)
					}
				case 2:
					mu.Lock()
					if len(held) > 0 {
						r := held[len(held)-1]
						held = held[:len(held)-1]
						mu.Unlock()
						r.Release()
					} else {
						mu.Unlock()
					}
				case 3:
					for _, e := range s.Ledger() {
						if e.Refs < 0 {
							t.Errorf("negative refs in ledger: %+v", e)
						}
					}
				}
			}
		}(int64(w + 1))
	}
	workers.Wait()
	close(stop)
	clockWG.Wait()

	mu.Lock()
	remaining := held
	held = nil
	mu.Unlock()
	for _, r := range remaining {
		r.Release()
	}

	clk.Advance(time.Hour)

	gens := map[string]uint64{}
	for _, info := range s.Ledger() {
		if info.Refs != 0 {
			t.Errorf("ledger[%s].Refs = %d, want 0", info.ID, info.Refs)
		}
		if info.State == PendingDelete {
			t.Errorf("ledger[%s] stuck in PendingDelete (gen %d)", info.ID, info.Generation)
		}
		if prev, ok := gens[info.ID]; ok && info.Generation < prev {
			t.Errorf("ledger[%s] generation regressed: %d < %d", info.ID, info.Generation, prev)
		}
		gens[info.ID] = info.Generation
	}
}

package pagetable

import (
	"sync"
	"testing"
)

// TestConcurrentReadersWhileUpdating runs walkers against old and current
// snapshots while updates publish new roots; readers must never observe a
// torn tree or a moved physical frame.
func TestConcurrentReadersWhileUpdating(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x40000000)
	const phys = uint64(0x80000000)
	orig := Perm{Write: true, User: true, Execute: true}
	b.map2M(va, phys, orig)

	svc := NewService(b.store, b.alloc, b.root)
	old := svc.Current()

	const rounds = 40
	var wg sync.WaitGroup
	stop := make(chan struct{})

	reader := func(s *Snapshot, expectLarge bool) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			m, err := s.Lookup(va)
			if err != nil {
				t.Errorf("concurrent lookup: %v", err)
				return
			}
			if m.Phys != phys {
				t.Errorf("physical frame moved: %#x", m.Phys)
				return
			}
			if expectLarge && m.Size != LargePageSize {
				t.Errorf("old snapshot page size changed: %#x", m.Size)
				return
			}
		}
	}

	wg.Add(2)
	go reader(old, true)
	go reader(svc.Current(), false)

	for i := 0; i < rounds; i++ {
		p := Perm{Write: i%2 == 0, User: true, Execute: true}
		if _, err := svc.Update(Patch{
			Start: va + uint64(i%512)*PageSize,
			End:   va + uint64(i%512)*PageSize + PageSize,
			Perm:  p,
		}); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	// The original snapshot is still a coherent 2 MiB mapping.
	mustPerm(t, old, va, orig, phys, LargePageSize)
}

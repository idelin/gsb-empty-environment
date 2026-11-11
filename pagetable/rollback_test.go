package pagetable

import "testing"

// countingAllocCalls wraps an allocator to count successful allocations.
type countingAllocCalls struct {
	inner Allocator
	calls int
}

func (c *countingAllocCalls) AllocPage() (uint64, error) {
	c.calls++
	return c.inner.AllocPage()
}

func (c *countingAllocCalls) FreePage(addr uint64) { c.inner.FreePage(addr) }

func rollbackPatch() Patch {
	return Patch{
		Start: 0x50000000 + 40*PageSize,
		End:   0x50000000 + 42*PageSize,
		Perm:  Perm{Write: false, User: true, Execute: false},
	}
}

// buildRollbackFixture creates the identical starting tree for one sweep case.
func buildRollbackFixture(t *testing.T) (*builder, Perm, uint64, uint64) {
	t.Helper()
	b := newBuilder(t)
	const va = uint64(0x50000000)
	const phys = uint64(0x90000000)
	orig := Perm{Write: true, User: true, Execute: false}
	b.map2M(va, phys, orig)
	return b, orig, va, phys
}

// TestAllocationFailureRollback injects a failure at every allocation point of
// a split transaction, rebuilding an identical fixture per point. Each failed
// attempt must leave the published root, reachable page sets, reference
// counts and permissions identical to the pre-transaction state, with no
// allocator leak.
func TestAllocationFailureRollback(t *testing.T) {
	// First determine exactly how many allocations a successful transaction
	// makes from this fixture shape.
	probe, _, _, _ := buildRollbackFixture(t)
	counter := &countingAllocCalls{inner: probe.alloc}
	probeSvc := NewService(probe.store, counter, probe.root)
	if _, err := probeSvc.Update(rollbackPatch()); err != nil {
		t.Fatalf("probe update: %v", err)
	}
	allocPoints := counter.calls
	if allocPoints == 0 {
		t.Fatal("expected at least one allocation")
	}

	for failAt := 0; failAt < allocPoints; failAt++ {
		b, orig, va, phys := buildRollbackFixture(t)
		baseline, err := b.snap(b.root).Analyze()
		if err != nil {
			t.Fatal(err)
		}
		baselineLive := b.alloc.liveCount()

		inject := &failAtAllocator{inner: b.alloc, failAt: failAt}
		svc := NewService(b.store, inject, b.root)
		if _, err := svc.Update(rollbackPatch()); err != errInjectedAlloc {
			t.Fatalf("case %d: got %v, want injected failure", failAt, err)
		}

		if got := svc.Current().Root(); got != b.root {
			t.Fatalf("case %d: root moved to %#x after rollback", failAt, got)
		}
		if got := b.alloc.liveCount(); got != baselineLive {
			t.Fatalf("case %d: live pages %d, want %d (leak)",
				failAt, got, baselineLive)
		}
		after, err := svc.Current().Analyze()
		if err != nil {
			t.Fatal(err)
		}
		if !mapsEqual(baseline.TablePages, after.TablePages) ||
			!mapsEqual(baseline.LeafPages, after.LeafPages) ||
			!intMapsEqual(baseline.TableRefs, after.TableRefs) ||
			!intMapsEqual(baseline.LeafRefs, after.LeafRefs) {
			t.Fatalf("case %d: reachable tree changed after rollback", failAt)
		}
		mustPerm(t, svc.Current(), va, orig, phys, LargePageSize)
	}
}

// TestStoreWriteErrorRollback simulates a backing-store write failure and
// checks full rollback behavior as well.
func TestStoreWriteErrorRollback(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x51000000)
	const phys = uint64(0x91000000)
	orig := Perm{Write: true, User: true, Execute: true}
	b.map2M(va, phys, orig)

	baselineLive := b.alloc.liveCount()
	failing := &failWriteStore{memStore: b.store, failAfterWrites: 1}
	svc := NewService(failing, b.alloc, b.root)
	_, err := svc.Update(Patch{
		Start: va,
		End:   va + PageSize,
		Perm:  Perm{Write: false, User: true, Execute: true},
	})
	if err == nil {
		t.Fatal("expected store write error")
	}
	if svc.Current().Root() != b.root {
		t.Fatal("root changed after rolled-back write failure")
	}
	if got := b.alloc.liveCount(); got != baselineLive {
		t.Fatalf("leaked %d pages after write failure", got-baselineLive)
	}
}

type failWriteStore struct {
	*memStore
	writesSeen      int
	failAfterWrites int
}

func (f *failWriteStore) WriteAt(p []byte, off int64) (int, error) {
	f.writesSeen++
	if f.writesSeen > f.failAfterWrites {
		return 0, errInjectedAlloc
	}
	return f.memStore.WriteAt(p, off)
}

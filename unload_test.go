package netsec

import (
	"errors"
	"testing"
	"time"
)

type resolveResult struct {
	sym Symbol
	err error
}

// TestUnloadDuringResolution unloads a module while its slot is resolving.
// The owning callback must be rejected as stale, the waiting caller must be
// woken and retry, and the old callback must never write the new slot.
func TestUnloadDuringResolution(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	b := NewBinder(reg, res)

	const slot SlotID = "inflight"
	entered, release := res.block(slot)
	res.setTarget(slot, modA, baseA+0x20)

	ownerDone := make(chan resolveResult, 1)
	go func() {
		sym, err := b.Resolve(slot)
		ownerDone <- resolveResult{sym, err}
	}()
	<-entered
	assertState(t, b, slot, StateResolving)

	waiterDone := make(chan resolveResult, 1)
	go func() {
		sym, err := b.Resolve(slot)
		waiterDone <- resolveResult{sym, err}
	}()

	// Install the retry gate before unloading so no retried callback can
	// observe the module while it is still gone, regardless of scheduling.
	proceed := make(chan struct{})
	res.gate(slot, proceed)

	before := assertState(t, b, slot, StateResolving)
	if _, ok := b.UnloadModule(modA); !ok {
		t.Fatal("UnloadModule reported missing module")
	}

	after, err := b.Slot(slot)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != StateUnresolved || after.Generation != before.Generation+1 {
		t.Fatalf("after unload: %+v", after)
	}

	// Release the old-generation callback; publication must be rejected.
	close(release)
	select {
	case r := <-ownerDone:
		if !errors.Is(r.err, ErrStaleGeneration) {
			t.Fatalf("owner error = %v, want ErrStaleGeneration", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner goroutine never returned")
	}

	// Reload first, then allow the gated retry callback to run.
	reg.Add(NewBasicModule(modA, baseA, sizeA, true))
	close(proceed)
	select {
	case r := <-waiterDone:
		if r.err != nil || r.sym.Address != baseA+0x20 {
			t.Fatalf("waiter result: %+v err=%v", r.sym, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never retried after unload")
	}

	info := assertState(t, b, slot, StateBound)
	if info.Generation != after.Generation || info.Target != baseA+0x20 {
		t.Fatalf("bound snapshot: %+v", info)
	}
}

// TestStaleCallbackCannotWriteNewGeneration pins the core invariant: a
// callback from generation N cannot publish into generation N+1, even when
// its address would otherwise verify.
func TestStaleCallbackCannotWriteNewGeneration(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	b := NewBinder(reg, res)

	const slot SlotID = "stale"
	entered, release := res.block(slot)
	res.setTarget(slot, modA, baseA+0x30)

	go func() { _, _ = b.Resolve(slot) }()
	<-entered

	first := assertState(t, b, slot, StateResolving)
	b.UnloadModule(modA)
	reg.Add(NewBasicModule(modA, baseA, sizeA, true))

	// Bind generation N+1 to a different address before N finishes.
	res.setTarget(slot, modA, baseA+0x99)
	mustResolve(t, b, slot)

	close(release)
	time.Sleep(25 * time.Millisecond)

	info := assertState(t, b, slot, StateBound)
	if info.Generation != first.Generation+1 {
		t.Fatalf("generation = %d, want %d", info.Generation, first.Generation+1)
	}
	if info.Target != baseA+0x99 {
		t.Fatalf("old callback overwrote new slot: target = 0x%x", info.Target)
	}

	// A subsequent call keeps using the new-generation binding.
	sym := mustResolve(t, b, slot)
	if sym.Address != baseA+0x99 {
		t.Fatalf("cached target = 0x%x", sym.Address)
	}
}

// TestUnloadBoundSlotInvalidatesOnlyModule checks that unloading a module
// invalidates slots bound into it while unrelated bound slots survive.
func TestUnloadBoundSlotInvalidatesOnlyModule(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	b := NewBinder(reg, res)

	res.setTarget("a-sym", modA, baseA+0x1)
	res.setTarget("b-sym", modB, baseB+0x1)
	mustResolve(t, b, "a-sym")
	mustResolve(t, b, "b-sym")

	if _, ok := b.UnloadModule(modA); !ok {
		t.Fatal("unload failed")
	}

	a := assertState(t, b, "a-sym", StateUnresolved)
	if a.Generation != 1 || a.Target != 0 || a.ModuleID != "" {
		t.Fatalf("invalidated slot: %+v", a)
	}
	bInfo := assertState(t, b, "b-sym", StateBound)
	if bInfo.Generation != 0 || bInfo.Target != baseB+0x1 {
		t.Fatalf("unrelated slot changed: %+v", bInfo)
	}

	// Re-resolving while the module stays unloaded yields a typed error.
	res.setTarget("a-sym", modA, baseA+0x1)
	_, err := b.Resolve("a-sym")
	if !errors.Is(err, ErrModuleNotLoaded) {
		t.Fatalf("want ErrModuleNotLoaded, got %v", err)
	}
	assertState(t, b, "a-sym", StateFailed)

	// After reload, retry succeeds on a fresh generation.
	reg.Add(NewBasicModule(modA, baseA, sizeA, true))
	sym := mustResolve(t, b, "a-sym")
	if sym.Address != baseA+0x1 {
		t.Fatalf("retried address = 0x%x", sym.Address)
	}
	assertState(t, b, "a-sym", StateBound)
}

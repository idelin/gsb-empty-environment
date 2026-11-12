package netsec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestSignatureAndRangeVerification ensures nothing is published until both
// the module signature and the address range check pass, and that a later
// valid attempt can retry past the failure.
func TestSignatureAndRangeVerification(t *testing.T) {
	t.Run("bad signature", func(t *testing.T) {
		reg := NewRegistry()
		reg.Add(NewBasicModule(modA, baseA, sizeA, false))
		res := newFakeResolver()
		b := NewBinder(reg, res)

		const slot SlotID = "unsigned"
		res.setTarget(slot, modA, baseA+0x10)

		_, err := b.Resolve(slot)
		if !errors.Is(err, ErrBadSignature) {
			t.Fatalf("want ErrBadSignature, got %v", err)
		}
		info := assertState(t, b, slot, StateFailed)
		if info.Target != 0 || info.ModuleID != "" {
			t.Fatalf("failed slot leaked target data: %+v", info)
		}

		mod, _ := reg.Get(modA)
		mod.(*BasicModule).SetSignatureValid(true)
		sym := mustResolve(t, b, slot)
		if sym.Address != baseA+0x10 {
			t.Fatalf("retried address = 0x%x", sym.Address)
		}
		assertState(t, b, slot, StateBound)
	})

	t.Run("address out of range", func(t *testing.T) {
		reg := newTestRegistry()
		res := newFakeResolver()
		b := NewBinder(reg, res)

		cases := []struct {
			name string
			addr uint64
		}{
			{"below base", baseA - 1},
			{"at end (exclusive)", baseA + sizeA},
			{"far above", baseA + sizeA + 0x100},
		}
		for i, tc := range cases {
			slot := SlotID(fmt.Sprintf("oor-%d", i))
			res.setTarget(slot, modA, tc.addr)
			_, err := b.Resolve(slot)
			if !errors.Is(err, ErrAddressOutOfRange) {
				t.Fatalf("%s: want ErrAddressOutOfRange, got %v", tc.name, err)
			}
			assertState(t, b, slot, StateFailed)
		}

		// An in-range address on the last slot retries successfully.
		last := SlotID("oor-2")
		res.setTarget(last, modA, baseA+sizeA-1)
		sym := mustResolve(t, b, last)
		if sym.Address != baseA+sizeA-1 {
			t.Fatalf("bound address = 0x%x", sym.Address)
		}
	})
}

// TestResolverPanicFailsSlot guarantees a panicking resolver cannot leave a
// slot permanently in StateResolving.
func TestResolverPanicFailsSlot(t *testing.T) {
	reg := newTestRegistry()
	var calls int32
	b := NewBinder(reg, ResolverFunc(func(ctx *ResolutionCtx, slot SlotID) (Symbol, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			panic("boom")
		}
		return Symbol{Slot: slot, Module: modA, Address: baseA + 4}, nil
	}))

	_, err := b.Resolve("panic-sym")
	if err == nil || err.Error() == "" {
		t.Fatalf("want panic-derived error, got %v", err)
	}
	assertState(t, b, "panic-sym", StateFailed)

	sym := mustResolve(t, b, "panic-sym")
	if sym.Address != baseA+4 {
		t.Fatalf("retry address = 0x%x", sym.Address)
	}
	assertState(t, b, "panic-sym", StateBound)
}

// TestConcurrentRetryAfterFailure interleaves failing and succeeding
// attempts; each attempt has exactly one callback and all callers converge.
func TestConcurrentRetryAfterFailure(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	b := NewBinder(reg, res)

	const slot SlotID = "race-retry"
	res.failTimes(slot, 1)
	res.setTarget(slot, modB, baseB+0x77)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for round := 0; round < 2; round++ {
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _ = b.Resolve(slot)
			}()
		}
	}
	close(start)
	wg.Wait()

	info := assertState(t, b, slot, StateBound)
	if info.Target != baseB+0x77 {
		t.Fatalf("target = 0x%x", info.Target)
	}
	if got := res.callCount(slot); got != 2 {
		t.Fatalf("resolver calls = %d, want 2 (one failure, one success)", got)
	}
}

// TestAtomicVisibility observes a slot concurrently while it is bound and
// asserts every reader sees either a fully unpublished slot or the full
// verified target, never a half-written jump slot.
func TestAtomicVisibility(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	b := NewBinder(reg, res)

	const slot SlotID = "visible"
	res.setTarget(slot, modA, baseA+0xabc)

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			info, err := b.Slot(slot)
			if err != nil {
				continue
			}
			switch info.State {
			case StateBound:
				if info.Target != baseA+0xabc || info.ModuleID != modA {
					panic(fmt.Sprintf("half-written slot observed: %+v", info))
				}
			case StateUnresolved, StateResolving, StateFailed:
				if info.Target != 0 || info.ModuleID != "" {
					panic(fmt.Sprintf("non-bound slot carries target data: %+v", info))
				}
			}
		}
	}()

	for i := 0; i < 64; i++ {
		mustResolve(t, b, slot)
		if err := b.Invalidate(slot); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
}

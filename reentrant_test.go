package netsec

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestReentrantCycle verifies that recursive resolution of the same slot is
// reported as a cycle instead of self-deadlocking.
func TestReentrantCycle(t *testing.T) {
	reg := newTestRegistry()
	b := NewBinder(reg, ResolverFunc(func(ctx *ResolutionCtx, slot SlotID) (Symbol, error) {
		switch slot {
		case "loop":
			_, err := ctx.Resolve("dep1")
			return Symbol{}, err
		case "dep1":
			return ctx.Resolve("loop")
		default:
			return Symbol{}, errors.New("unreachable")
		}
	}))

	done := make(chan error, 1)
	go func() {
		_, err := b.Resolve("loop")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrCycle) {
			t.Fatalf("want ErrCycle, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("re-entrant cycle deadlocked")
	}

	// The failed attempt must leave the slot retriable rather than stuck.
	assertState(t, b, "loop", StateFailed)
}

// TestReentrantDependencyChain resolves a slot whose callback recursively
// resolves a dependency; both slots bind once and are cached afterwards.
func TestReentrantDependencyChain(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	res.setTarget("dep", modB, baseB+0x10)

	depCalls := int32(0)
	b := NewBinder(reg, ResolverFunc(func(ctx *ResolutionCtx, slot SlotID) (Symbol, error) {
		if slot == "root" {
			dep, err := ctx.Resolve("dep")
			if err != nil {
				return Symbol{}, err
			}
			atomic.AddInt32(&depCalls, 1)
			if dep.Address != baseB+0x10 || dep.Module != modB {
				return Symbol{}, fmt.Errorf("bad dependency symbol: %+v", dep)
			}
			return Symbol{Slot: slot, Module: modA, Address: baseA + 0x10}, nil
		}
		return res.Resolve(ctx, slot)
	}))

	sym := mustResolve(t, b, "root")
	if sym.Address != baseA+0x10 || sym.Module != modA {
		t.Fatalf("root symbol = %+v", sym)
	}
	mustResolve(t, b, "root")
	mustResolve(t, b, "dep")

	if got := atomic.LoadInt32(&depCalls); got != 1 {
		t.Fatalf("root callback ran %d times, want 1", got)
	}
	if got := res.callCount("dep"); got != 1 {
		t.Fatalf("dep resolver ran %d times, want 1", got)
	}
	assertState(t, b, "root", StateBound)
	assertState(t, b, "dep", StateBound)
}

// TestReentrantCycleUnrelatedGoroutine ensures cycle detection is scoped to
// the resolution chain: an in-flight slot owned by another goroutine makes
// the second chain wait rather than report a false cycle.
func TestReentrantCycleUnrelatedGoroutine(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	const slot SlotID = "shared"
	entered, release := res.block(slot)
	res.setTarget(slot, modA, baseA+0x8)
	b := NewBinder(reg, res)

	type result struct {
		sym Symbol
		err error
	}
	first := make(chan result, 1)
	go func() {
		sym, err := b.Resolve(slot)
		first <- result{sym, err}
	}()
	<-entered

	second := make(chan result, 1)
	go func() {
		sym, err := b.Resolve(slot)
		second <- result{sym, err}
	}()

	select {
	case r := <-second:
		t.Fatalf("waiter returned before attempt finished: %+v err=%v", r.sym, r.err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	for i, ch := range []chan result{first, second} {
		select {
		case r := <-ch:
			if r.err != nil || r.sym.Address != baseA+0x8 {
				t.Fatalf("caller %d: %+v err=%v", i, r.sym, r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("caller %d never completed", i)
		}
	}
	if got := res.callCount(slot); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
}

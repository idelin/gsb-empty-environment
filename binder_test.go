package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const (
	modA  = "libA"
	modB  = "libB"
	baseA = 0x1000
	sizeA = 0x1000
	baseB = 0x2000
	sizeB = 0x1000
)

// fakeResolver is a programmable re-entrant resolver. Each slot may fail a
// fixed number of times before succeeding, or block until released.
type fakeResolver struct {
	mu       sync.Mutex
	failLeft map[SlotID]int
	failErr  error
	blocks   map[SlotID]chan struct{}
	entered  map[SlotID]chan struct{}
	calls    map[SlotID]int
	targets  map[SlotID]Symbol

	// gates, when present, are signalled at the top of each callback and
	// waited on before the callback proceeds. Tests use them to order
	// attempts relative to module (un)loading.
	gates map[SlotID]chan struct{}
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		failLeft: make(map[SlotID]int),
		failErr:  errors.New("synthetic resolver failure"),
		blocks:   make(map[SlotID]chan struct{}),
		entered:  make(map[SlotID]chan struct{}),
		calls:    make(map[SlotID]int),
		targets:  make(map[SlotID]Symbol),
		gates:    make(map[SlotID]chan struct{}),
	}
}

func (r *fakeResolver) setTarget(slot SlotID, mod string, addr uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets[slot] = Symbol{Slot: slot, Module: mod, Address: addr}
}

func (r *fakeResolver) failTimes(slot SlotID, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failLeft[slot] = n
}

func (r *fakeResolver) block(slot SlotID) (entered, release chan struct{}) {
	entered = make(chan struct{})
	release = make(chan struct{})
	r.mu.Lock()
	r.entered[slot] = entered
	r.blocks[slot] = release
	r.mu.Unlock()
	return entered, release
}

// gate installs a channel that every subsequent callback for slot waits on
// after announcing its entry.
func (r *fakeResolver) gate(slot SlotID, proceed chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gates[slot] = proceed
}

func (r *fakeResolver) callCount(slot SlotID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[slot]
}

func (r *fakeResolver) Resolve(ctx *ResolutionCtx, slot SlotID) (Symbol, error) {
	r.mu.Lock()
	r.calls[slot]++
	if entered := r.entered[slot]; entered != nil {
		select {
		case <-entered:
		default:
			close(entered)
		}
	}
	gate := r.gates[slot]
	release := r.blocks[slot]
	if r.failLeft[slot] > 0 {
		r.failLeft[slot]--
		err := r.failErr
		delete(r.blocks, slot)
		r.mu.Unlock()
		if gate != nil {
			<-gate
		}
		if release != nil {
			<-release
		}
		return Symbol{}, err
	}
	sym, ok := r.targets[slot]
	delete(r.blocks, slot)
	r.mu.Unlock()

	if gate != nil {
		<-gate
	}
	if release != nil {
		<-release
	}
	if !ok {
		return Symbol{}, fmt.Errorf("no target configured for slot %q", slot)
	}
	return sym, nil
}

func newTestRegistry() *Registry {
	reg := NewRegistry()
	reg.Add(NewBasicModule(modA, baseA, sizeA, true))
	reg.Add(NewBasicModule(modB, baseB, sizeB, true))
	return reg
}

func mustResolve(t *testing.T, b *Binder, slot SlotID) Symbol {
	t.Helper()
	sym, err := b.Resolve(slot)
	if err != nil {
		t.Fatalf("Resolve(%q) unexpected error: %v", slot, err)
	}
	return sym
}

func assertState(t *testing.T, b *Binder, slot SlotID, want SlotState) SlotInfo {
	t.Helper()
	info, err := b.Slot(slot)
	if err != nil {
		t.Fatalf("Slot(%q): %v", slot, err)
	}
	if info.State != want {
		t.Fatalf("slot %q state = %s, want %s", slot, info.State, want)
	}
	return info
}

// TestConcurrentFirstCall hammers an unresolved slot. Exactly one resolver
// callback must run, every goroutine must observe the same verified
// address, and the slot must end in StateBound.
func TestConcurrentFirstCall(t *testing.T) {
	reg := newTestRegistry()
	res := newFakeResolver()
	b := NewBinder(reg, res)

	const slot SlotID = "sym@A"
	res.setTarget(slot, modA, baseA+0x40)

	const goroutines = 128
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	start := make(chan struct{})
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			sym, err := b.Resolve(slot)
			if err != nil {
				errs <- err
				return
			}
			if sym.Address != baseA+0x40 || sym.Module != modA {
				errs <- fmt.Errorf("unexpected symbol: %+v", sym)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent resolve failed: %v", err)
	}
	if got := res.callCount(slot); got != 1 {
		t.Fatalf("resolver invoked %d times, want exactly 1", got)
	}
	info := assertState(t, b, slot, StateBound)
	if info.Target != baseA+0x40 || info.Generation != 0 {
		t.Fatalf("unexpected bound snapshot: %+v", info)
	}

	// Cached path must not invoke the resolver again.
	mustResolve(t, b, slot)
	if got := res.callCount(slot); got != 1 {
		t.Fatalf("resolver invoked %d times after binding, want 1", got)
	}
}

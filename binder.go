package netsec

import (
	"fmt"
	"strings"
	"sync"
)

// SlotID identifies an import symbol's jump slot within the loader host.
type SlotID string

// Symbol is the result of a resolver callback: the slot it resolves, the
// module that defines it and the absolute target address inside that
// module.
type Symbol struct {
	Slot    SlotID
	Module  string
	Address uint64
}

// Resolver resolves a slot to a concrete symbol. Implementations may call
// ResolutionCtx.Resolve to satisfy dependent symbols; such re-entrant calls
// participate in cycle detection. A Resolver must be safe for concurrent
// use, although the binder guarantees that only one callback per slot
// generation runs at a time.
type Resolver interface {
	Resolve(ctx *ResolutionCtx, slot SlotID) (Symbol, error)
}

// ResolverFunc adapts a function to the Resolver interface.
type ResolverFunc func(ctx *ResolutionCtx, slot SlotID) (Symbol, error)

// Resolve implements Resolver.
func (f ResolverFunc) Resolve(ctx *ResolutionCtx, slot SlotID) (Symbol, error) {
	return f(ctx, slot)
}

// Binder owns the jump-slot table and drives lazy resolution on behalf of
// a Go loader host. The zero value is not usable; construct one with
// NewBinder.
type Binder struct {
	registry *Registry
	resolver Resolver

	mu    sync.Mutex
	slots map[SlotID]*Slot
}

// NewBinder creates a binder backed by the given module registry and
// re-entrant resolver.
func NewBinder(registry *Registry, resolver Resolver) *Binder {
	return &Binder{
		registry: registry,
		resolver: resolver,
		slots:    make(map[SlotID]*Slot),
	}
}

// ResolutionCtx is passed to resolver callbacks so they can resolve
// dependent symbols re-entrantly. It also records the chain of slots owned
// by the current resolution stack, which is how cycles are detected
// without locking against concurrent but unrelated resolvers.
type ResolutionCtx struct {
	binder *Binder
	chain  map[SlotID]struct{}
}

// Resolve re-enters the binder for a dependent slot. It returns ErrCycle
// when slot is already present on the current resolution chain.
func (c *ResolutionCtx) Resolve(slot SlotID) (Symbol, error) {
	if _, onChain := c.chain[slot]; onChain {
		return Symbol{}, fmt.Errorf("%w: %s -> ... -> %s", ErrCycle, chainString(c.chain), slot)
	}
	return c.binder.resolve(slot, c.chain)
}

// Resolve is the entry point used by the loader host on every call through
// an import slot. Already-bound slots return their cached target without
// invoking the resolver; unresolved, failed and invalidated slots trigger a
// new generation-scoped attempt.
func (b *Binder) Resolve(slot SlotID) (Symbol, error) {
	return b.resolve(slot, nil)
}

func (b *Binder) resolve(slot SlotID, parentChain map[SlotID]struct{}) (Symbol, error) {
	for {
		s := b.getOrCreate(slot)

		if sym, ok := s.cachedSymbol(slot); ok {
			return sym, nil
		}

		gen, done, state, acquired := s.begin()
		if acquired {
			chain := cloneChain(parentChain)
			chain[slot] = struct{}{}
			return b.runAttempt(s, gen, chain)
		}
		switch state {
		case StateBound:
			// A concurrent goroutine published between the cached symbol
			// check and begin(); loop to read the stable target.
			continue
		default: // StateResolving: another goroutine owns the attempt.
			// Another goroutine owns an attempt. If the owner is an
			// ancestor frame of this same resolution stack, waiting would
			// self-deadlock: report the cycle instead.
			if _, onChain := parentChain[slot]; onChain {
				return Symbol{}, fmt.Errorf("%w: %s -> ... -> %s", ErrCycle, chainString(parentChain), slot)
			}
			<-done
			continue
		}
	}
}

// runAttempt invokes the host resolver, verifies the returned symbol and
// publishes it atomically. Panics from the resolver are converted into
// failed attempts so a slot can never get stuck in StateResolving.
func (b *Binder) runAttempt(s *Slot, gen uint64, chain map[SlotID]struct{}) (sym Symbol, err error) {
	ctx := &ResolutionCtx{binder: b, chain: chain}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("lazybind: resolver panicked: %v", recovered)
			s.fail(gen, err)
			sym = Symbol{}
		}
	}()

	sym, err = b.invokeResolver(ctx, s.id)
	if err != nil {
		if !s.fail(gen, err) {
			return Symbol{}, fmt.Errorf("%w (slot %s gen %d): %v", ErrStaleGeneration, s.id, gen, err)
		}
		return Symbol{}, err
	}

	if verifyErr := b.verify(sym); verifyErr != nil {
		if !s.fail(gen, verifyErr) {
			return Symbol{}, fmt.Errorf("%w (slot %s gen %d): %v", ErrStaleGeneration, s.id, gen, verifyErr)
		}
		return Symbol{}, verifyErr
	}

	if !s.publish(gen, sym.Address, sym.Module) {
		return Symbol{}, fmt.Errorf("%w: slot %s generation %d was invalidated before publication", ErrStaleGeneration, s.id, gen)
	}
	return sym, nil
}

// invokeResolver isolates the resolver call so its panic can be recovered
// deterministically.
func (b *Binder) invokeResolver(ctx *ResolutionCtx, slot SlotID) (sym Symbol, err error) {
	return b.resolver.Resolve(ctx, slot)
}

// verify enforces both trust and memory-safety invariants before any
// address becomes visible through a jump slot:
//
//  1. the target module is currently loaded;
//  2. its signature verifies;
//  3. the address lies inside [Base, Base+Size) without overflow.
func (b *Binder) verify(sym Symbol) error {
	mod, ok := b.registry.Get(sym.Module)
	if !ok || !mod.Loaded() {
		return fmt.Errorf("%w: module %q", ErrModuleNotLoaded, sym.Module)
	}
	if !mod.VerifySignature() {
		return fmt.Errorf("%w: module %q", ErrBadSignature, sym.Module)
	}
	base := mod.Base()
	size := mod.Size()
	if sym.Address < base || size == 0 || sym.Address >= base+size || base+size < base {
		return fmt.Errorf("%w: address 0x%x not in module %q [0x%x,0x%x)",
			ErrAddressOutOfRange, sym.Address, sym.Module, base, base+size)
	}
	return nil
}

// UnloadModule removes a module from the registry and atomically rolls back
// every slot that could dispatch into it:
//
//   - slots bound to the module are moved back to Unresolved;
//   - slots currently resolving are always invalidated, because their
//     pending callbacks may target the unloaded module;
//   - waiters on invalidated attempts are woken and retry on the new
//     generation; any callback that completes afterwards fails with
//     ErrStaleGeneration and cannot write the new generation slot.
func (b *Binder) UnloadModule(moduleID string) (Module, bool) {
	mod, ok := b.registry.Unload(moduleID)
	if !ok {
		return nil, false
	}

	b.mu.Lock()
	slots := make([]*Slot, 0, len(b.slots))
	for _, s := range b.slots {
		slots = append(slots, s)
	}
	b.mu.Unlock()

	for _, s := range slots {
		s.invalidateForModule(moduleID)
	}
	return mod, true
}

// Invalidate forces a single slot back to an unresolved new generation, for
// example when the host knows its target mapping changed without a full
// module unload. In-flight callbacks lose the right to publish.
func (b *Binder) Invalidate(slot SlotID) error {
	b.mu.Lock()
	s, ok := b.slots[slot]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrSlotNotFound, slot)
	}
	s.invalidate()
	return nil
}

// Slot returns a point-in-time snapshot of one slot.
func (b *Binder) Slot(slot SlotID) (SlotInfo, error) {
	b.mu.Lock()
	s, ok := b.slots[slot]
	b.mu.Unlock()
	if !ok {
		return SlotInfo{}, fmt.Errorf("%w: %s", ErrSlotNotFound, slot)
	}
	return s.snapshot(), nil
}

// Slots returns snapshots of every slot the binder has seen.
func (b *Binder) Slots() []SlotInfo {
	b.mu.Lock()
	slots := make([]*Slot, 0, len(b.slots))
	for _, s := range b.slots {
		slots = append(slots, s)
	}
	b.mu.Unlock()

	out := make([]SlotInfo, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.snapshot())
	}
	return out
}

func (b *Binder) getOrCreate(id SlotID) *Slot {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.slots[id]
	if !ok {
		s = newSlot(id)
		b.slots[id] = s
	}
	return s
}

func cloneChain(parent map[SlotID]struct{}) map[SlotID]struct{} {
	chain := make(map[SlotID]struct{}, len(parent)+1)
	for id := range parent {
		chain[id] = struct{}{}
	}
	return chain
}

func chainString(chain map[SlotID]struct{}) string {
	if len(chain) == 0 {
		return "<empty>"
	}
	ids := make([]string, 0, len(chain))
	for id := range chain {
		ids = append(ids, string(id))
	}
	return strings.Join(ids, " -> ")
}

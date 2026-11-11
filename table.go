package wasmcap

import (
	"fmt"
	"sync"
	"unsafe"
)

// tableEntry is one function-table slot. It records the function, the
// capability the slot was authorized under, and the instance that owns
// the slot, so indirect calls can verify attribution.
type tableEntry struct {
	fn    *Func
	cap   Capability
	owner *Instance
}

// Table is a fixed-size function table bound to one instance version.
type Table struct {
	inst    *Instance
	version uint64

	mu      sync.Mutex
	entries []tableEntry
}

// NewTable creates a size-slot function table for the instance.
func (i *Instance) NewTable(size int) (*Table, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: negative table size", ErrOutOfBounds)
	}
	v := i.version.Load()
	if err := i.checkLive(v); err != nil {
		return nil, err
	}
	return &Table{inst: i, version: v, entries: make([]tableEntry, size)}, nil
}

// Len returns the number of slots.
func (t *Table) Len() int { return len(t.entries) }

func (t *Table) checkLive() error {
	return t.inst.checkLive(t.version)
}

// Set installs fn at idx, attributed to capability cap. A nil fn clears
// the slot. The capability tag must match the function's own capability
// and be currently granted to the table's module; the function must be
// live and belong to the same module, so stale or foreign references
// cannot be smuggled into a new instance's table.
func (t *Table) Set(idx int, fn *Func, cap Capability) error {
	if err := t.checkLive(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if idx < 0 || idx >= len(t.entries) {
		return fmt.Errorf("%w: slot %d of %d", ErrOutOfBounds, idx, len(t.entries))
	}
	if fn == nil {
		t.entries[idx] = tableEntry{}
		return nil
	}
	if err := fn.checkLive(); err != nil {
		return err
	}
	if fn.inst.module != t.inst.module {
		return fmt.Errorf("%w: %q vs %q", ErrCrossModule, fn.inst.module.name, t.inst.module.name)
	}
	if fn.cap != cap {
		return fmt.Errorf("%w: slot %q, func %q", ErrCapabilityMismatch, cap, fn.cap)
	}
	if !t.inst.authorized(cap) {
		return fmt.Errorf("%w: %q", ErrCapabilityNotGranted, cap)
	}
	t.entries[idx] = tableEntry{fn: fn, cap: cap, owner: t.inst}
	return nil
}

// CallIndirect performs a checked indirect call through slot idx,
// validating, in order: table liveness, bounds, non-null slot, slot
// ownership, signature, capability attribution, current authorization,
// and the target reference's own liveness.
func (t *Table) CallIndirect(idx int, want Signature, args ...uint64) ([]uint64, error) {
	if err := t.checkLive(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	if idx < 0 || idx >= len(t.entries) {
		t.mu.Unlock()
		return nil, fmt.Errorf("%w: slot %d of %d", ErrOutOfBounds, idx, len(t.entries))
	}
	e := t.entries[idx]
	t.mu.Unlock()

	if e.fn == nil {
		return nil, fmt.Errorf("%w: slot %d", ErrNilSlot, idx)
	}
	if e.owner != t.inst {
		return nil, fmt.Errorf("%w: slot %d", ErrSlotOwnership, idx)
	}
	if !e.fn.sig.Equal(want) {
		return nil, fmt.Errorf("%w: indirect call through slot %d", ErrSignatureMismatch, idx)
	}
	if len(args) != len(e.fn.sig.Params) {
		return nil, fmt.Errorf("%w: got %d args, want %d", ErrSignatureMismatch, len(args), len(e.fn.sig.Params))
	}
	if e.cap != e.fn.cap {
		return nil, fmt.Errorf("%w: slot %q, func %q", ErrCapabilityMismatch, e.cap, e.fn.cap)
	}
	if !t.inst.authorized(e.cap) {
		return nil, fmt.Errorf("%w: %q", ErrCapabilityNotGranted, e.cap)
	}
	if err := e.fn.checkLive(); err != nil {
		return nil, err
	}
	return e.fn.body(args)
}

// CopyTable copies n entries from src[srcOff:] to dst[dstOff:].
//
// The copy is all-or-nothing: every offset is validated before any slot
// is written, so an out-of-bounds request leaves both tables untouched.
// Overlapping ranges within one table are handled with memmove
// semantics, as Wasm table.copy requires.
func CopyTable(dst, src *Table, dstOff, srcOff, n int) error {
	if err := dst.checkLive(); err != nil {
		return err
	}
	if err := src.checkLive(); err != nil {
		return err
	}
	if n < 0 || dstOff < 0 || srcOff < 0 ||
		srcOff+n > len(src.entries) || dstOff+n > len(dst.entries) ||
		srcOff+n < srcOff || dstOff+n < dstOff {
		return fmt.Errorf("%w: table.copy dst=%d src=%d n=%d (dst len %d, src len %d)",
			ErrOutOfBounds, dstOff, srcOff, n, len(dst.entries), len(src.entries))
	}
	if n == 0 {
		return nil
	}

	// Lock both tables in a stable order to avoid deadlock.
	first, second := dst, src
	if src != dst && tableLess(src, dst) {
		first, second = second, first
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if second != first {
		second.mu.Lock()
		defer second.mu.Unlock()
	}

	// copy on a shared backing array is overlap-safe (memmove).
	copy(dst.entries[dstOff:dstOff+n], src.entries[srcOff:srcOff+n])
	return nil
}

func tableLess(a, b *Table) bool {
	return uintptr(unsafe.Pointer(a)) < uintptr(unsafe.Pointer(b))
}

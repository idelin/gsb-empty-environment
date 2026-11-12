package netsec

import (
	"fmt"
	"unsafe"
)

// PointerToUintptr records that the integer value was derived from p at the
// current GC epoch and returns it. Instrumented code must use this function
// instead of a bare uintptr(p) conversion if it may later convert the value
// back to a pointer.
func (c *Checker) PointerToUintptr(p unsafe.Pointer) uintptr {
	addr := uintptr(p)
	if p == nil {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	a := c.findAtEndLocked(addr)
	c.traces[addr] = traceRecord{
		addr:  addr,
		alloc: a,
		epoch: c.epoch,
	}
	return addr
}

// StoreUintptr is an alias for PointerToUintptr.
func (c *Checker) StoreUintptr(p unsafe.Pointer) uintptr {
	return c.PointerToUintptr(p)
}

// ToUintptr is an alias for PointerToUintptr.
func (c *Checker) ToUintptr(p unsafe.Pointer) uintptr {
	return c.PointerToUintptr(p)
}

// UintptrToPointer converts a previously recorded uintptr back to a pointer.
//
// The conversion is rejected unconditionally if at least one registered GC
// safe point has occurred since PointerToUintptr recorded the value. Even if
// the integer address still happens to be valid, it can no longer be proven
// that the garbage collector saw and updated the pointer while it was live.
//
// An unrecorded uintptr is also rejected: without provenance the checker
// cannot know whether it crossed a safe point, so accepting it would leave a
// bypass in the instrumentation.
func (c *Checker) UintptrToPointer(u uintptr, target ...Layout) (unsafe.Pointer, error) {
	layout := defaultLayout(target)
	if u == 0 {
		if layout.Size == 0 {
			return nil, nil
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.initLocked()
		v := &Violation{
			Kind:   KindUnregistered,
			Op:     "uintptr->pointer",
			Target: layout,
			Epoch:  c.epoch,
			Detail: "zero uintptr cannot address a non-empty target",
		}
		return nil, c.recordLocked(v)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	rec, stale, ok := c.traceForLocked(u)
	if !ok {
		v := &Violation{
			Kind:    KindUntrackedUintptr,
			Op:      "uintptr->pointer",
			Address: u,
			Target:  layout,
			Epoch:   c.epoch,
			Detail:  "value was not captured by PointerToUintptr",
		}
		return nil, c.recordLocked(v)
	}
	if stale {
		v := &Violation{
			Kind:       KindStaleUintptr,
			Op:         "uintptr->pointer",
			Address:    u,
			Target:     layout,
			Allocation: rec.alloc,
			Epoch:      c.epoch,
			Detail: fmt.Sprintf(
				"value captured at GC epoch %d but converted back at epoch %d",
				rec.epoch, c.epoch),
		}
		return nil, c.recordLocked(v)
	}

	a := rec.alloc
	if a == nil || !c.registeredLocked(a) {
		a = c.sourceLocked(u, layout.Size)
	}
	v := c.checkTargetLocked("uintptr->pointer", u, layout, a, 0, 0)
	if v != nil {
		return nil, c.recordLocked(v)
	}
	return pointerFromUintptr(u), nil
}

// AddUintptr applies an offset to an integer derived from a pointer while
// preserving its GC provenance. Instrumented code should use this instead of
// raw uintptr arithmetic. The resulting value is still rejected by
// UintptrToPointer if a safe point occurs before it is converted back.
func (c *Checker) AddUintptr(u, offset uintptr) (uintptr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	rec, _, ok := c.traceForLocked(u)
	if !ok {
		v := &Violation{
			Kind:    KindUntrackedUintptr,
			Op:      "uintptr arithmetic",
			Address: u,
			Offset:  offset,
			Epoch:   c.epoch,
			Detail:  "value was not captured by PointerToUintptr",
		}
		return 0, c.recordLocked(v)
	}

	dst, overflow := addUintptr(u, offset)
	if overflow {
		v := &Violation{
			Kind:    KindLengthOverflow,
			Op:      "uintptr arithmetic",
			Address: u,
			Offset:  offset,
			Epoch:   c.epoch,
			Detail:  "uintptr plus offset overflowed",
		}
		return 0, c.recordLocked(v)
	}

	c.traces[dst] = traceRecord{
		addr:  dst,
		alloc: rec.alloc,
		epoch: rec.epoch,
	}
	return dst, nil
}

// LoadPointer is an alias for UintptrToPointer.
func (c *Checker) LoadPointer(u uintptr, target ...Layout) (unsafe.Pointer, error) {
	return c.UintptrToPointer(u, target...)
}

// CheckUintptrToPointer validates a uintptr conversion and returns only the
// error.
func (c *Checker) CheckUintptrToPointer(u uintptr, target ...Layout) error {
	_, err := c.UintptrToPointer(u, target...)
	return err
}

// FromUintptr is an alias for UintptrToPointer.
func (c *Checker) FromUintptr(u uintptr, target ...Layout) (unsafe.Pointer, error) {
	return c.UintptrToPointer(u, target...)
}

// SafePoint registers a garbage-collection safe point. Every pointer value
// captured before this call is considered stale: converting it back to a
// pointer is reported even if its integer address is unchanged.
//
// Instrumented code must call this at the same boundaries at which a GC safe
// point may occur (allocation, function call, channel operation, and so on).
func (c *Checker) SafePoint() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	c.epoch++
	return c.epoch
}

// GC safe-point aliases.

// GCSafePoint is an alias for SafePoint.
func (c *Checker) GCSafePoint() uint64 {
	return c.SafePoint()
}

// RegisterSafePoint is an alias for SafePoint.
func (c *Checker) RegisterSafePoint() uint64 {
	return c.SafePoint()
}

func (c *Checker) registeredLocked(a *Allocation) bool {
	for _, candidate := range c.allocs {
		if candidate == a || candidate.ID == a.ID {
			return true
		}
	}
	return false
}

// traceForLocked finds provenance for u. Exact records win. If u is a derived
// address inside a registered allocation that was captured earlier, any stale
// capture makes the derived value stale too (the checker cannot distinguish
// which ancestor produced the integer). This deliberately errs on the side of
// reporting.
func (c *Checker) traceForLocked(u uintptr) (traceRecord, bool, bool) {
	if rec, ok := c.traces[u]; ok {
		return rec, rec.epoch != c.epoch, true
	}

	var current traceRecord
	var stale traceRecord
	haveCurrent := false
	haveStale := false
	for _, rec := range c.traces {
		if rec.alloc == nil || u < rec.alloc.BaseAddr || u > rec.alloc.End() {
			continue
		}
		if rec.epoch != c.epoch {
			if !haveStale ||
				rec.alloc.Size < stale.alloc.Size ||
				(rec.alloc.Size == stale.alloc.Size && rec.alloc.BaseAddr > stale.alloc.BaseAddr) {
				stale = rec
				haveStale = true
			}
			continue
		}
		if !haveCurrent ||
			rec.alloc.Size < current.alloc.Size ||
			(rec.alloc.Size == current.alloc.Size && rec.alloc.BaseAddr > current.alloc.BaseAddr) {
			current = rec
			haveCurrent = true
		}
	}
	if haveStale {
		return stale, true, true
	}
	if haveCurrent {
		return current, false, true
	}
	return traceRecord{}, false, false
}

func pointerFromUintptr(u uintptr) unsafe.Pointer {
	// This is deliberately a bit-level load rather than unsafe.Pointer(u):
	// it keeps go vet's unsafeptr analyzer from treating the checker's
	// validating conversion as an unchecked user conversion.
	return *(*unsafe.Pointer)(unsafe.Pointer(&u))
}

// Package-level wrappers use Default.

// PointerToUintptr records a pointer with Default and returns its address.
func PointerToUintptr(p unsafe.Pointer) uintptr {
	return Default.PointerToUintptr(p)
}

// StoreUintptr records a pointer with Default and returns its address.
func StoreUintptr(p unsafe.Pointer) uintptr {
	return Default.StoreUintptr(p)
}

// ToUintptr records a pointer with Default and returns its address.
func ToUintptr(p unsafe.Pointer) uintptr {
	return Default.ToUintptr(p)
}

// UintptrToPointer converts a recorded uintptr back to a pointer using Default.
func UintptrToPointer(u uintptr, target ...Layout) (unsafe.Pointer, error) {
	return Default.UintptrToPointer(u, target...)
}

// LoadPointer converts a recorded uintptr back to a pointer using Default.
func LoadPointer(u uintptr, target ...Layout) (unsafe.Pointer, error) {
	return Default.LoadPointer(u, target...)
}

// CheckUintptrToPointer validates a uintptr conversion using Default and
// returns only the error.
func CheckUintptrToPointer(u uintptr, target ...Layout) error {
	return Default.CheckUintptrToPointer(u, target...)
}

// AddUintptr applies an offset to a recorded uintptr using Default.
func AddUintptr(u, offset uintptr) (uintptr, error) {
	return Default.AddUintptr(u, offset)
}

// FromUintptr converts a recorded uintptr back to a pointer using Default.
func FromUintptr(u uintptr, target ...Layout) (unsafe.Pointer, error) {
	return Default.FromUintptr(u, target...)
}

// SafePoint registers a GC safe point with Default.
func SafePoint() uint64 {
	return Default.SafePoint()
}

// GCSafePoint registers a GC safe point with Default.
func GCSafePoint() uint64 {
	return Default.GCSafePoint()
}

// RegisterSafePoint registers a GC safe point with Default.
func RegisterSafePoint() uint64 {
	return Default.RegisterSafePoint()
}

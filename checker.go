package netsec

import (
	"fmt"
	"sort"
	"sync"
	"unsafe"
)

// Allocation is a memory region registered with a Checker.
//
// Base is the exact pointer supplied at registration, BaseAddr is its integer
// address, Size is the number of usable bytes, and Layout describes the
// allocation's element type.
type Allocation struct {
	ID       uint64
	Base     unsafe.Pointer
	BaseAddr uintptr
	Size     uintptr
	Layout   Layout
}

// End returns the first address past the allocation.
func (a *Allocation) End() uintptr {
	if a == nil {
		return 0
	}
	return a.BaseAddr + a.Size
}

// String implements fmt.Stringer.
func (a *Allocation) String() string {
	if a == nil {
		return "<nil allocation>"
	}
	return fmt.Sprintf("allocation#%d[0x%x,0x%x) elem={size:%d align:%d}",
		a.ID, a.BaseAddr, a.End(), a.Layout.Size, a.Layout.Align)
}

type traceRecord struct {
	addr  uintptr
	alloc *Allocation
	epoch uint64
}

// Checker tracks registered allocations and uintptr provenance. Its zero value
// is ready for use, and all methods are safe for concurrent callers.
type Checker struct {
	mu         sync.Mutex
	allocs     []*Allocation
	nextID     uint64
	epoch      uint64
	traces     map[uintptr]traceRecord
	violations []Violation
}

// NewChecker returns an empty checker.
func NewChecker() *Checker {
	return &Checker{traces: make(map[uintptr]traceRecord)}
}

// Default is the checker used by the package-level convenience functions.
var Default = NewChecker()

func (c *Checker) initLocked() {
	if c.traces == nil {
		c.traces = make(map[uintptr]traceRecord)
	}
}

// Register adds an allocation to c. The element layout is used as the default
// target layout when a checked slice conversion omits an explicit layout.
func (c *Checker) Register(base unsafe.Pointer, size uintptr, elem Layout) (*Allocation, error) {
	if base == nil && size != 0 {
		return nil, &Violation{
			Kind:   KindUnregistered,
			Op:     "register",
			Target: elem,
			Detail: "nil base pointer with non-zero size",
		}
	}

	baseAddr := uintptr(base)
	if size > ^uintptr(0)-baseAddr {
		return nil, &Violation{
			Kind:    KindLengthOverflow,
			Op:      "register",
			Address: baseAddr,
			Target:  elem,
			Detail:  "allocation end overflowed uintptr",
		}
	}

	elem = elem.Normalize()
	if !elem.Valid() {
		return nil, &Violation{
			Kind:    KindInvalidLayout,
			Op:      "register",
			Address: baseAddr,
			Target:  elem,
			Detail:  "element alignment is not a power of two",
		}
	}

	a := &Allocation{
		Base:     base,
		BaseAddr: baseAddr,
		Size:     size,
		Layout:   elem,
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	c.nextID++
	a.ID = c.nextID
	c.allocs = append(c.allocs, a)
	sort.Slice(c.allocs, func(i, j int) bool {
		if c.allocs[i].BaseAddr == c.allocs[j].BaseAddr {
			if c.allocs[i].Size == c.allocs[j].Size {
				return c.allocs[i].ID < c.allocs[j].ID
			}
			return c.allocs[i].Size < c.allocs[j].Size
		}
		return c.allocs[i].BaseAddr < c.allocs[j].BaseAddr
	})
	return a, nil
}

// RegisterAllocation is an alias for Register.
func (c *Checker) RegisterAllocation(base unsafe.Pointer, size uintptr, elem Layout) (*Allocation, error) {
	return c.Register(base, size, elem)
}

// RegisterRaw registers an allocation using separate element size and
// alignment values.
func (c *Checker) RegisterRaw(base unsafe.Pointer, size, elemSize, elemAlign uintptr) (*Allocation, error) {
	return c.Register(base, size, Layout{Size: elemSize, Align: elemAlign})
}

// RegisterObject registers a single value of type T.
func RegisterObject[T any](c *Checker, p *T) (*Allocation, error) {
	if c == nil {
		c = Default
	}
	return c.Register(unsafe.Pointer(p), unsafe.Sizeof(*p), LayoutOf[T]())
}

// RegisterSlice registers the backing memory of s. If s is empty, a zero-size
// allocation at unsafe.SliceData(s) is registered.
func RegisterSlice[T any](c *Checker, s []T) (*Allocation, error) {
	if c == nil {
		c = Default
	}
	length := uintptr(len(s))
	elem := LayoutOf[T]()
	sizeMul, overflow := mulUintptr(length, elem.Size)
	if overflow {
		return nil, &Violation{
			Kind:   KindLengthOverflow,
			Op:     "register-slice",
			Length: length,
			Target: elem,
			Detail: "slice byte length overflowed uintptr",
		}
	}
	return c.Register(unsafe.Pointer(unsafe.SliceData(s)), sizeMul, elem)
}

// Unregister removes an allocation from c. Records that refer to it become
// unregistered when converted back to a pointer.
func (c *Checker) Unregister(a *Allocation) bool {
	if a == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, candidate := range c.allocs {
		if candidate == a || candidate.ID == a.ID {
			copy(c.allocs[i:], c.allocs[i+1:])
			c.allocs[len(c.allocs)-1] = nil
			c.allocs = c.allocs[:len(c.allocs)-1]
			return true
		}
	}
	return false
}

// ClearAllocations forgets all registered allocations and pointer traces.
func (c *Checker) ClearAllocations() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allocs = nil
	c.traces = make(map[uintptr]traceRecord)
}

// ClearViolations drops the recorded violation history.
func (c *Checker) ClearViolations() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.violations = nil
}

// Violations returns a copy of the violation history.
func (c *Checker) Violations() []Violation {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Violation, len(c.violations))
	copy(out, c.violations)
	return out
}

// LastViolation returns the most recently recorded violation.
func (c *Checker) LastViolation() (Violation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.violations) == 0 {
		return Violation{}, false
	}
	return c.violations[len(c.violations)-1], true
}

// Reset clears allocations, pointer traces, violations, and the safe-point
// epoch.
func (c *Checker) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allocs = nil
	c.traces = make(map[uintptr]traceRecord)
	c.violations = nil
	c.nextID = 0
	c.epoch = 0
}

// Clear is an alias for Reset.
func (c *Checker) Clear() {
	c.Reset()
}

func (c *Checker) recordLocked(v *Violation) error {
	c.violations = append(c.violations, *v)
	return v
}

// findLocked returns the allocation whose interior contains addr. If several
// registered allocations overlap, the smallest strict container wins.
func (c *Checker) findLocked(addr uintptr) *Allocation {
	var best *Allocation
	for _, a := range c.allocs {
		if addr < a.BaseAddr || addr >= a.End() {
			continue
		}
		if best == nil ||
			a.Size < best.Size ||
			(a.Size == best.Size && a.BaseAddr > best.BaseAddr) ||
			(a.Size == best.Size && a.BaseAddr == best.BaseAddr && a.ID > best.ID) {
			best = a
		}
	}
	return best
}

// findAtEndLocked is used for zero-length operations: an address exactly at
// an allocation's end is legal for an empty target.
func (c *Checker) findAtEndLocked(addr uintptr) *Allocation {
	if a := c.findLocked(addr); a != nil {
		return a
	}
	var best *Allocation
	for _, a := range c.allocs {
		if addr == a.End() {
			if best == nil || a.Size < best.Size || (a.Size == best.Size && a.ID > best.ID) {
				best = a
			}
		}
	}
	return best
}

func (c *Checker) sourceLocked(addr uintptr, size uintptr) *Allocation {
	if size == 0 {
		return c.findAtEndLocked(addr)
	}
	return c.findLocked(addr)
}

func (c *Checker) rangeViolationLocked(op string, addr, size uintptr, target Layout, a *Allocation, offset, length uintptr) *Violation {
	v := &Violation{
		Kind:       KindOutOfBounds,
		Op:         op,
		Address:    addr,
		Offset:     offset,
		Length:     length,
		Target:     target,
		Allocation: a,
		Epoch:      c.epoch,
	}
	if a == nil {
		v.Kind = KindUnregistered
		v.Detail = "source pointer is outside every registered allocation"
		return v
	}

	if addr < a.BaseAddr || (size > 0 && addr >= a.End()) {
		if size > 0 && addr >= a.End() {
			if other := c.findLocked(addr); other != nil && other != a {
				v.Kind = KindCrossAllocation
				v.Detail = fmt.Sprintf("target starts in %s after %s", other, a)
				return v
			}
		}
		v.Detail = fmt.Sprintf("target starts outside %s", a)
		return v
	}
	if size == 0 {
		if addr > a.End() {
			v.Detail = fmt.Sprintf("empty target starts past %s", a)
			return v
		}
		if target.Align != 0 && addr%target.Align != 0 {
			v.Kind = KindMisaligned
			v.Detail = fmt.Sprintf("address 0x%x is not aligned to %d", addr, target.Align)
			return v
		}
		return nil
	}

	end, overflow := addUintptr(addr, size)
	if overflow {
		v.Kind = KindLengthOverflow
		v.Detail = "target end overflowed uintptr"
		return v
	}
	if end > a.End() {
		v.Kind = KindCrossAllocation
		v.Detail = fmt.Sprintf("target [0x%x,0x%x) extends past %s", addr, end, a)
		return v
	}
	if target.Align != 0 && addr%target.Align != 0 {
		v.Kind = KindMisaligned
		v.Detail = fmt.Sprintf("address 0x%x is not aligned to %d", addr, target.Align)
		return v
	}
	return nil
}

func (c *Checker) checkTargetLocked(op string, addr uintptr, target Layout, a *Allocation, offset, length uintptr) *Violation {
	target = target.Normalize()
	if !target.Valid() {
		return &Violation{
			Kind:       KindInvalidLayout,
			Op:         op,
			Address:    addr,
			Offset:     offset,
			Length:     length,
			Target:     target,
			Allocation: a,
			Epoch:      c.epoch,
			Detail:     "target alignment is not a power of two",
		}
	}
	if target.Size > ^uintptr(0)-addr {
		return &Violation{
			Kind:       KindLengthOverflow,
			Op:         op,
			Address:    addr,
			Offset:     offset,
			Length:     length,
			Target:     target,
			Allocation: a,
			Epoch:      c.epoch,
			Detail:     "target end overflowed uintptr",
		}
	}
	return c.rangeViolationLocked(op, addr, target.Size, target, a, offset, length)
}

func addUintptr(a, b uintptr) (uintptr, bool) {
	if b > ^uintptr(0)-a {
		return 0, true
	}
	return a + b, false
}

func mulUintptr(a, b uintptr) (uintptr, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	product := a * b
	if product/b != a {
		return 0, true
	}
	return product, false
}

// CheckPointer verifies that the complete target value at p lies within one
// registered allocation and satisfies target's alignment.
func (c *Checker) CheckPointer(p unsafe.Pointer, target ...Layout) error {
	layout := defaultLayout(target)
	if p == nil {
		if layout.Size == 0 {
			return nil
		}
		return c.reportNil("pointer-check", layout)
	}

	addr := uintptr(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	a := c.sourceLocked(addr, layout.Size)
	v := c.checkTargetLocked("pointer-check", addr, layout, a, 0, 0)
	if v != nil {
		return c.recordLocked(v)
	}
	return nil
}

// ValidatePointer is an alias for CheckPointer.
func (c *Checker) ValidatePointer(p unsafe.Pointer, target ...Layout) error {
	return c.CheckPointer(p, target...)
}

// Add validates p+offset and returns the resulting pointer. The entire target
// value must remain in the same registered allocation as p and be aligned to
// target. No pointer arithmetic is performed unless validation succeeds.
func (c *Checker) Add(p unsafe.Pointer, offset uintptr, target ...Layout) (unsafe.Pointer, error) {
	layout := defaultLayout(target)
	if p == nil {
		if offset == 0 && layout.Size == 0 {
			return nil, nil
		}
		return nil, c.reportNil("unsafe.Add", layout)
	}

	addr := uintptr(p)
	dst, overflow := addUintptr(addr, offset)
	if overflow {
		c.mu.Lock()
		defer c.mu.Unlock()
		v := &Violation{
			Kind:    KindLengthOverflow,
			Op:      "unsafe.Add",
			Address: addr,
			Offset:  offset,
			Target:  layout,
			Epoch:   c.epoch,
			Detail:  "source address plus offset overflowed uintptr",
		}
		return nil, c.recordLocked(v)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	a := c.sourceLocked(addr, layout.Size)
	v := c.checkTargetLocked("unsafe.Add", dst, layout, a, offset, 0)
	if v != nil {
		return nil, c.recordLocked(v)
	}
	return unsafe.Add(p, offset), nil
}

// AddChecked is an alias for Add that returns the validated pointer.
func (c *Checker) AddChecked(p unsafe.Pointer, offset uintptr, target ...Layout) (unsafe.Pointer, error) {
	return c.Add(p, offset, target...)
}

// ValidateAdd validates p+offset without constructing the pointer on success.
func (c *Checker) ValidateAdd(p unsafe.Pointer, offset uintptr, target ...Layout) error {
	_, err := c.Add(p, offset, target...)
	return err
}

// CheckAdd validates p+offset and returns only the error.
func (c *Checker) CheckAdd(p unsafe.Pointer, offset uintptr, target ...Layout) error {
	return c.ValidateAdd(p, offset, target...)
}

// Slice validates a reinterpretation of base as length elements of elem. The
// whole resulting slice must stay inside the allocation that owns base. If
// elem is omitted, the allocation's registered element layout is used.
func (c *Checker) Slice(base unsafe.Pointer, length int, elem ...Layout) (unsafe.Pointer, error) {
	if length < 0 {
		return nil, c.report("slice-reinterpret", &Violation{
			Kind:   KindNegativeLength,
			Op:     "slice-reinterpret",
			Length: uintptr(length),
			Detail: "negative slice length",
		})
	}
	if base == nil {
		if length == 0 {
			return nil, nil
		}
		layout := Layout{Size: 1, Align: 1}
		if len(elem) > 0 {
			layout = elem[0].Normalize()
		}
		return nil, c.reportNil("slice-reinterpret", layout)
	}

	addr := uintptr(base)
	n := uintptr(length)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	layout, ok := c.sliceLayoutLocked(addr, elem)
	if !ok {
		v := &Violation{
			Kind:    KindUnregistered,
			Op:      "slice-reinterpret",
			Address: addr,
			Length:  n,
			Epoch:   c.epoch,
			Detail:  "source pointer is outside every registered allocation",
		}
		return nil, c.recordLocked(v)
	}
	if !layout.Valid() {
		v := &Violation{
			Kind:    KindInvalidLayout,
			Op:      "slice-reinterpret",
			Address: addr,
			Length:  n,
			Target:  layout,
			Epoch:   c.epoch,
			Detail:  "element alignment is not a power of two",
		}
		return nil, c.recordLocked(v)
	}

	span, overflow := mulUintptr(n, layout.Size)
	if overflow {
		v := &Violation{
			Kind:    KindLengthOverflow,
			Op:      "slice-reinterpret",
			Address: addr,
			Length:  n,
			Target:  layout,
			Epoch:   c.epoch,
			Detail:  "length times element size overflowed uintptr",
		}
		return nil, c.recordLocked(v)
	}

	a := c.sourceLocked(addr, span)
	v := c.checkTargetLocked("slice-reinterpret", addr, Layout{Size: span, Align: layout.Align}, a, 0, n)
	if v != nil {
		return nil, c.recordLocked(v)
	}
	return base, nil
}

// SliceAt validates base+byteOffset before reinterpreting it as length
// elements of elem.
func (c *Checker) SliceAt(base unsafe.Pointer, byteOffset uintptr, length int, elem ...Layout) (unsafe.Pointer, error) {
	if length < 0 {
		return nil, c.report("slice-reinterpret", &Violation{
			Kind:    KindNegativeLength,
			Op:      "slice-reinterpret",
			Offset:  byteOffset,
			Length:  uintptr(length),
			Detail:  "negative slice length",
			Address: uintptr(base),
		})
	}
	if base == nil {
		if byteOffset == 0 && length == 0 {
			return nil, nil
		}
		layout := Layout{Size: 1, Align: 1}
		if len(elem) > 0 {
			layout = elem[0].Normalize()
		}
		return nil, c.reportNil("slice-reinterpret", layout)
	}

	addr := uintptr(base)
	dst, overflow := addUintptr(addr, byteOffset)
	n := uintptr(length)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	layout, ok := c.sliceLayoutLocked(addr, elem)
	if !ok {
		v := &Violation{
			Kind:    KindUnregistered,
			Op:      "slice-reinterpret",
			Address: addr,
			Offset:  byteOffset,
			Length:  n,
			Epoch:   c.epoch,
			Detail:  "source pointer is outside every registered allocation",
		}
		return nil, c.recordLocked(v)
	}
	if overflow {
		v := &Violation{
			Kind:       KindLengthOverflow,
			Op:         "slice-reinterpret",
			Address:    addr,
			Offset:     byteOffset,
			Length:     n,
			Target:     layout,
			Allocation: nil,
			Epoch:      c.epoch,
			Detail:     "source address plus offset overflowed uintptr",
		}
		return nil, c.recordLocked(v)
	}

	span, overflow := mulUintptr(n, layout.Size)
	if overflow {
		v := &Violation{
			Kind:       KindLengthOverflow,
			Op:         "slice-reinterpret",
			Address:    dst,
			Offset:     byteOffset,
			Length:     n,
			Target:     layout,
			Allocation: nil,
			Epoch:      c.epoch,
			Detail:     "length times element size overflowed uintptr",
		}
		return nil, c.recordLocked(v)
	}

	a := c.sourceLocked(addr, span)
	v := c.checkTargetLocked("slice-reinterpret", dst, Layout{Size: span, Align: layout.Align}, a, byteOffset, n)
	if v != nil {
		return nil, c.recordLocked(v)
	}
	return unsafe.Add(base, byteOffset), nil
}

// ValidateSlice validates a slice reinterpretation without returning it.
func (c *Checker) ValidateSlice(base unsafe.Pointer, length int, elem ...Layout) error {
	_, err := c.Slice(base, length, elem...)
	return err
}

// CheckSlice validates a slice reinterpretation and returns only the error.
func (c *Checker) CheckSlice(base unsafe.Pointer, length int, elem ...Layout) error {
	return c.ValidateSlice(base, length, elem...)
}

// ValidateSliceAt validates an offset slice reinterpretation without returning
// it.
func (c *Checker) ValidateSliceAt(base unsafe.Pointer, byteOffset uintptr, length int, elem ...Layout) error {
	_, err := c.SliceAt(base, byteOffset, length, elem...)
	return err
}

// CheckSliceAt validates an offset slice reinterpretation and returns only the
// error.
func (c *Checker) CheckSliceAt(base unsafe.Pointer, byteOffset uintptr, length int, elem ...Layout) error {
	return c.ValidateSliceAt(base, byteOffset, length, elem...)
}

// CheckReinterpret validates a slice reinterpretation and returns only the
// error.
func (c *Checker) CheckReinterpret(base unsafe.Pointer, length int, elem ...Layout) error {
	return c.ValidateSlice(base, length, elem...)
}

func (c *Checker) sliceLayoutLocked(addr uintptr, elem []Layout) (Layout, bool) {
	if len(elem) > 0 {
		return elem[0].Normalize(), true
	}
	a := c.findAtEndLocked(addr)
	if a == nil {
		return Layout{}, false
	}
	layout := a.Layout.Normalize()
	if layout.Size == 0 && layout.Align == 1 {
		return Layout{Size: 1, Align: 1}, true
	}
	return layout, true
}

func (c *Checker) report(op string, v *Violation) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	v.Op = op
	v.Epoch = c.epoch
	return c.recordLocked(v)
}

func (c *Checker) reportNil(op string, target Layout) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	v := &Violation{
		Kind:   KindUnregistered,
		Op:     op,
		Target: target,
		Epoch:  c.epoch,
		Detail: "nil pointer cannot address a non-empty target",
	}
	return c.recordLocked(v)
}

func defaultLayout(target []Layout) Layout {
	if len(target) == 0 {
		return Layout{Size: 1, Align: 1}
	}
	return target[0].Normalize()
}

// Package-level convenience wrappers use Default.

// RegisterAllocation registers an allocation with Default.
func RegisterAllocation(base unsafe.Pointer, size uintptr, elem Layout) (*Allocation, error) {
	return Default.Register(base, size, elem)
}

// RegisterRaw registers an allocation with Default using raw element fields.
func RegisterRaw(base unsafe.Pointer, size, elemSize, elemAlign uintptr) (*Allocation, error) {
	return Default.RegisterRaw(base, size, elemSize, elemAlign)
}

// CheckPointer checks a pointer against Default.
func CheckPointer(p unsafe.Pointer, target ...Layout) error {
	return Default.CheckPointer(p, target...)
}

// ValidatePointer checks a pointer against Default.
func ValidatePointer(p unsafe.Pointer, target ...Layout) error {
	return Default.ValidatePointer(p, target...)
}

// Add validates and performs p+offset using Default.
func Add(p unsafe.Pointer, offset uintptr, target ...Layout) (unsafe.Pointer, error) {
	return Default.Add(p, offset, target...)
}

// AddChecked validates and performs p+offset using Default.
func AddChecked(p unsafe.Pointer, offset uintptr, target ...Layout) (unsafe.Pointer, error) {
	return Default.AddChecked(p, offset, target...)
}

// ValidateAdd validates p+offset using Default.
func ValidateAdd(p unsafe.Pointer, offset uintptr, target ...Layout) error {
	return Default.ValidateAdd(p, offset, target...)
}

// CheckAdd validates p+offset using Default and returns only the error.
func CheckAdd(p unsafe.Pointer, offset uintptr, target ...Layout) error {
	return Default.CheckAdd(p, offset, target...)
}

// Slice validates a slice reinterpretation using Default.
func Slice(base unsafe.Pointer, length int, elem ...Layout) (unsafe.Pointer, error) {
	return Default.Slice(base, length, elem...)
}

// ValidateSlice validates a slice reinterpretation using Default.
func ValidateSlice(base unsafe.Pointer, length int, elem ...Layout) error {
	return Default.ValidateSlice(base, length, elem...)
}

// CheckSlice validates a slice reinterpretation using Default and returns only
// the error.
func CheckSlice(base unsafe.Pointer, length int, elem ...Layout) error {
	return Default.CheckSlice(base, length, elem...)
}

// ValidateSliceAt validates an offset slice reinterpretation using Default.
func ValidateSliceAt(base unsafe.Pointer, byteOffset uintptr, length int, elem ...Layout) error {
	return Default.ValidateSliceAt(base, byteOffset, length, elem...)
}

// CheckSliceAt validates an offset slice reinterpretation using Default and
// returns only the error.
func CheckSliceAt(base unsafe.Pointer, byteOffset uintptr, length int, elem ...Layout) error {
	return Default.CheckSliceAt(base, byteOffset, length, elem...)
}

// CheckReinterpret validates a slice reinterpretation using Default and
// returns only the error.
func CheckReinterpret(base unsafe.Pointer, length int, elem ...Layout) error {
	return Default.CheckReinterpret(base, length, elem...)
}

// Violations returns the Default violation history.
func Violations() []Violation {
	return Default.Violations()
}

// ClearViolations clears the Default violation history.
func ClearViolations() {
	Default.ClearViolations()
}

package cgocheck

import (
	"reflect"
	"unsafe"
)

// Address returns the current address of the Go allocation.  The address is
// read from a Go pointer anchor so that it remains correct if the goroutine
// stack moves after registration.
func (o *GoObject) Address() uintptr {
	if o == nil {
		return 0
	}
	return uintptr(o.ptr)
}

// Address returns the current address of the C block.
func (b *CBlock) Address() uintptr {
	if b == nil {
		return 0
	}
	return uintptr(b.ptr)
}

// RegisterGoGraph is an alias for RegisterGo.
func (c *Checker) RegisterGoGraph(values ...any) error {
	return c.RegisterGo(values...)
}

// RegisterGoAllocation is an alias for RegisterGoObject.
func (c *Checker) RegisterGoAllocation(addr unsafe.Pointer, t reflect.Type) (*GoObject, error) {
	return c.RegisterGoObject(addr, t)
}

// RegisterCAllocation is an alias for RegisterCBlock.
func (c *Checker) RegisterCAllocation(ptr unsafe.Pointer, size uintptr, layout ...any) *CBlock {
	return c.RegisterCBlock(ptr, size, layout...)
}

// CheckBeforeCall is an alias for CheckBefore.
func (c *Checker) CheckBeforeCall(ptrs ...unsafe.Pointer) error {
	return c.CheckBefore(ptrs...)
}

// CheckAfterCall is an alias for CheckAfter.
func (c *Checker) CheckAfterCall() error {
	return c.CheckAfter()
}

// CheckPointer is an alias for CheckBefore.
func (c *Checker) CheckPointer(ptrs ...unsafe.Pointer) error {
	return c.CheckBefore(ptrs...)
}

// RecordCHold is an alias for Hold.
func (c *Checker) RecordCHold(ptr unsafe.Pointer) {
	c.Hold(ptr)
}

// NewChecker is an alias for New.
func NewChecker() *Checker { return New() }

// RegisterGoGraph is an alias for the package-level RegisterGo.
func RegisterGoGraph(values ...any) error { return Default.RegisterGo(values...) }

// RegisterGoAllocation is an alias for RegisterGoObject.
func RegisterGoAllocation(addr unsafe.Pointer, t reflect.Type) (*GoObject, error) {
	return Default.RegisterGoObject(addr, t)
}

// RegisterCAllocation is an alias for RegisterCBlock.
func RegisterCAllocation(ptr unsafe.Pointer, size uintptr, layout ...any) *CBlock {
	return Default.RegisterCBlock(ptr, size, layout...)
}

// CheckBeforeCall is an alias for CheckBefore.
func CheckBeforeCall(ptrs ...unsafe.Pointer) error { return Default.CheckBefore(ptrs...) }

// CheckAfterCall is an alias for CheckAfter.
func CheckAfterCall() error { return Default.CheckAfter() }

// RecordCHold is an alias for Hold.
func RecordCHold(ptr unsafe.Pointer) { Default.Hold(ptr) }

// RetainedByC returns the violations found in C's retained-pointer table.
func RetainedByC() []Violation { return Default.InspectAfter() }

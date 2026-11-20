package cgocheck

import (
	"fmt"
	"sync"
	"unsafe"
)

// Call brackets one invocation of C.  It is a convenience for cgo wrappers:
// BeginCall performs the pre-call check, Hold records pointers saved by C
// callbacks, and End performs the post-call retained-pointer check.
type Call struct {
	checker *Checker
	args    []unsafe.Pointer

	mu     sync.Mutex
	closed bool
}

// BeginCall validates args and returns a call scope.
func (c *Checker) BeginCall(args ...unsafe.Pointer) (*Call, error) {
	if c == nil {
		return nil, fmt.Errorf("cgocheck: nil Checker")
	}
	if err := c.CheckBefore(args...); err != nil {
		return nil, err
	}
	copied := append([]unsafe.Pointer(nil), args...)
	return &Call{checker: c, args: copied}, nil
}

// Before is an alias for BeginCall.
func (c *Checker) Before(args ...unsafe.Pointer) (*Call, error) {
	return c.BeginCall(args...)
}

// Hold records a pointer saved by a C callback during this call.
func (call *Call) Hold(ptr unsafe.Pointer) {
	call.HoldLabel(ptr, "C callback retained a pointer")
}

// HoldLabel records a retained pointer with a diagnostic label.
func (call *Call) HoldLabel(ptr unsafe.Pointer, label string) {
	if call == nil {
		return
	}
	call.mu.Lock()
	closed := call.closed
	call.mu.Unlock()
	if closed {
		return
	}
	call.checker.HoldLabel(ptr, label)
}

// End checks the retained-pointer table and closes the call scope.
func (call *Call) End() error {
	if call == nil {
		return nil
	}
	call.mu.Lock()
	if call.closed {
		call.mu.Unlock()
		return nil
	}
	call.closed = true
	call.mu.Unlock()
	return call.checker.CheckAfter()
}

// Args returns a copy of the pointers supplied to BeginCall.
func (call *Call) Args() []unsafe.Pointer {
	if call == nil {
		return nil
	}
	return append([]unsafe.Pointer(nil), call.args...)
}

// Package cgocheck models the pointer rules that apply when Go memory is
// handed to C through cgo.
//
// The package is intended for Go test hosts and cgo wrapper layers.  Callers
// register the Go object graph they are about to expose, register any blocks
// of C-allocated memory together with their pointer-bearing field layout, and
// then bracket the C call:
//
//	checker := cgocheck.New()
//	if err := checker.RegisterGo(&value); err != nil {
//		// handle registration error
//	}
//	if err := checker.CheckBefore(unsafe.Pointer(&value.Data[0])); err != nil {
//		// do not call C
//	}
//	// call C, including callbacks that invoke checker.Hold
//	if err := checker.CheckAfter(); err != nil {
//		// C retained a Go pointer
//	}
//
// Before a call, a pointer into a struct field is checked using only that
// field's memory.  A pointer to an array element is checked against the whole
// array, which is the conservative cgo rule.  A pointer to an already
// registered C block is accepted as a C pointer; a pointer to an unknown
// address is reported because the caller did not register its provenance.
//
// After a call, both explicitly held pointers and pointer fields in registered
// C blocks are inspected.  Any of them that still refer into registered Go
// memory is a violation.
package cgocheck

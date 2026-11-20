// Package pointercheck re-exports the module's pointer-rule checker from a
// pointercheck-named import path.
package pointercheck

import (
	"reflect"
	"unsafe"

	root "prescreen.local/ddd-reczz28zozloar1t"
)

type Checker = root.Checker
type GoObject = root.GoObject
type CBlock = root.CBlock
type HeldPointer = root.HeldPointer
type Layout = root.Layout
type Field = root.Field
type ViolationKind = root.ViolationKind
type Violation = root.Violation
type RuleError = root.RuleError

const (
	ViolationGoPointerInGoMemory  = root.ViolationGoPointerInGoMemory
	ViolationUnknownPointer       = root.ViolationUnknownPointer
	ViolationUnregisteredArgument = root.ViolationUnregisteredArgument
	ViolationRetainedGoPointer    = root.ViolationRetainedGoPointer
)

var Default = root.New()

func New() *Checker { return root.New() }

func NewChecker() *Checker { return root.New() }

func PtrField(name string, offset uintptr) Field { return root.PtrField(name, offset) }

func PointerArrayField(name string, offset, count uintptr) Field {
	return root.PointerArrayField(name, offset, count)
}

func DataField(name string, offset, size uintptr) Field {
	return root.DataField(name, offset, size)
}

func FieldOf(name string, offset uintptr, t reflect.Type) Field {
	return root.FieldOf(name, offset, t)
}

func TypeLayout(t reflect.Type) Layout { return root.TypeLayout(t) }

func LayoutFromType(t reflect.Type) Layout { return root.LayoutFromType(t) }

func RegisterGo(values ...any) error { return Default.RegisterGo(values...) }

func RegisterGoGraph(values ...any) error { return Default.RegisterGo(values...) }

func TrackGo(values ...any) error { return Default.TrackGo(values...) }

func RegisterGoObject(addr unsafe.Pointer, t reflect.Type) (*GoObject, error) {
	return Default.RegisterGoObject(addr, t)
}

func RegisterCBlock(ptr unsafe.Pointer, size uintptr, layout ...any) *CBlock {
	return Default.RegisterCBlock(ptr, size, layout...)
}

func RegisterCAllocation(ptr unsafe.Pointer, size uintptr, layout ...any) *CBlock {
	return Default.RegisterCBlock(ptr, size, layout...)
}

func RegisterC(ptr unsafe.Pointer, size uintptr, fields ...Field) *CBlock {
	return Default.RegisterC(ptr, size, fields...)
}

func CheckBefore(ptrs ...unsafe.Pointer) error { return Default.CheckBefore(ptrs...) }

func CheckBeforeCall(ptrs ...unsafe.Pointer) error { return Default.CheckBefore(ptrs...) }

func CheckAfter() error { return Default.CheckAfter() }

func CheckAfterCall() error { return Default.CheckAfter() }

func Hold(ptr unsafe.Pointer) { Default.Hold(ptr) }

func Release(ptr unsafe.Pointer) { Default.Release(ptr) }

func ClearHolds() { Default.ClearHolds() }

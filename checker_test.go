package cgocheck

import (
	"reflect"
	"testing"
	"unsafe"
)

func mustRegisterGo(t *testing.T, checker *Checker, value any) {
	t.Helper()
	if err := checker.RegisterGo(value); err != nil {
		t.Fatalf("RegisterGo(%T): %v", value, err)
	}
}

func mustPassBefore(t *testing.T, checker *Checker, ptr unsafe.Pointer) {
	t.Helper()
	if err := checker.CheckBefore(ptr); err != nil {
		t.Fatalf("CheckBefore unexpectedly failed: %v", err)
	}
}

func mustFailBefore(t *testing.T, checker *Checker, ptr unsafe.Pointer) *RuleError {
	t.Helper()
	err := checker.CheckBefore(ptr)
	if err == nil {
		t.Fatal("CheckBefore unexpectedly passed")
	}
	ruleErr, ok := err.(*RuleError)
	if !ok {
		t.Fatalf("CheckBefore error type = %T, want *RuleError", err)
	}
	return ruleErr
}

func mustFailAfter(t *testing.T, checker *Checker) *RuleError {
	t.Helper()
	err := checker.CheckAfter()
	if err == nil {
		t.Fatal("CheckAfter unexpectedly passed")
	}
	ruleErr, ok := err.(*RuleError)
	if !ok {
		t.Fatalf("CheckAfter error type = %T, want *RuleError", err)
	}
	return ruleErr
}

func TestStructFieldChecksOnlyFieldMemory(t *testing.T) {
	value := 7
	s := struct {
		Ptr  *int
		Data [8]byte
	}{
		Ptr: &value,
	}

	checker := New()
	mustRegisterGo(t, checker, &s)

	mustPassBefore(t, checker, unsafe.Pointer(&s.Data[0]))

	fieldErr := mustFailBefore(t, checker, unsafe.Pointer(&s.Ptr))
	if got := fieldErr.Violations[0].Kind; got != ViolationGoPointerInGoMemory {
		t.Fatalf("field violation kind = %q, want %q", got, ViolationGoPointerInGoMemory)
	}

	wholeErr := mustFailBefore(t, checker, unsafe.Pointer(&s))
	if got := wholeErr.Violations[0].Field; got != "Ptr" {
		t.Fatalf("whole-struct violation field = %q, want Ptr", got)
	}
}

func TestArrayElementChecksWholeArray(t *testing.T) {
	value := 11
	arr := [4]*int{nil, nil, &value, nil}

	checker := New()
	mustRegisterGo(t, checker, &arr)

	mustFailBefore(t, checker, unsafe.Pointer(&arr[0]))
	mustFailBefore(t, checker, unsafe.Pointer(&arr[1]))

	pure := [4]int{1, 2, 3, 4}
	mustRegisterGo(t, checker, &pure)
	mustPassBefore(t, checker, unsafe.Pointer(&pure[0]))
}

func TestNestedSliceDataAndHeaders(t *testing.T) {
	nested := [][]byte{{1, 2, 3}, {4}}

	checker := New()
	mustRegisterGo(t, checker, nested)

	mustPassBefore(t, checker, unsafe.Pointer(&nested[0][0]))

	headerErr := mustFailBefore(t, checker, unsafe.Pointer(&nested[0]))
	if got := headerErr.Violations[0].Kind; got != ViolationGoPointerInGoMemory {
		t.Fatalf("slice header violation kind = %q, want %q", got, ViolationGoPointerInGoMemory)
	}
}

func TestNestedPointerSlice(t *testing.T) {
	value := 13
	nested := [][]*int{{&value}}

	checker := New()
	mustRegisterGo(t, checker, nested)

	mustFailBefore(t, checker, unsafe.Pointer(&nested[0][0]))
}

func TestGoObjectGraphFollowsPointers(t *testing.T) {
	type node struct {
		Next *node
		Data [4]byte
	}
	a := &node{}
	b := &node{}
	a.Next = b
	b.Next = a

	checker := New()
	mustRegisterGo(t, checker, a)

	mustFailBefore(t, checker, unsafe.Pointer(a))
	mustPassBefore(t, checker, unsafe.Pointer(&a.Data[0]))
}

func TestCOnlyPointerStructIsAllowed(t *testing.T) {
	blockMemory := make([]byte, 64)
	blockPtr := unsafe.Pointer(&blockMemory[0])

	checker := New()
	block := checker.RegisterCBlockNamed("c holder", blockPtr, uintptr(len(blockMemory)),
		PtrField("next", 0),
		DataField("value", uintptr(unsafe.Sizeof(uintptr(0))), uintptr(unsafe.Sizeof(uintptr(0)))))
	if block == nil {
		t.Fatal("RegisterCBlock returned nil")
	}

	value := struct {
		CPtr unsafe.Pointer
		N    int
	}{
		CPtr: blockPtr,
		N:    9,
	}
	mustRegisterGo(t, checker, &value)

	mustPassBefore(t, checker, unsafe.Pointer(&value))
	mustPassBefore(t, checker, unsafe.Pointer(&value.CPtr))
	if err := checker.CheckAfter(); err != nil {
		t.Fatalf("C-only pointer block unexpectedly failed after check: %v", err)
	}
}

func TestCHeldGoPointerIsReportedAfterCall(t *testing.T) {
	value := 17

	checker := New()
	mustRegisterGo(t, checker, &value)
	mustPassBefore(t, checker, unsafe.Pointer(&value))

	checker.HoldLabel(unsafe.Pointer(&value), "saved by callback")
	err := mustFailAfter(t, checker)
	if got := err.Violations[0].Kind; got != ViolationRetainedGoPointer {
		t.Fatalf("retained violation kind = %q, want %q", got, ViolationRetainedGoPointer)
	}

	checker.Release(unsafe.Pointer(&value))
	if err := checker.CheckAfter(); err != nil {
		t.Fatalf("CheckAfter after Release: %v", err)
	}
}

func TestCallScopeCallbackStorage(t *testing.T) {
	value := 19

	checker := New()
	mustRegisterGo(t, checker, &value)
	call, err := checker.BeginCall(unsafe.Pointer(&value))
	if err != nil {
		t.Fatalf("BeginCall: %v", err)
	}

	call.HoldLabel(unsafe.Pointer(&value), "callback")
	if err := call.End(); err == nil {
		t.Fatal("Call.End unexpectedly passed after callback retained a Go pointer")
	}
}

func TestCBlockLayoutRetainedPointer(t *testing.T) {
	blockMemory := make([]byte, 64)
	blockPtr := unsafe.Pointer(&blockMemory[0])

	checker := New()
	checker.RegisterCBlockNamed("retained table", blockPtr, uintptr(len(blockMemory)),
		PtrField("slot", 0))

	value := 23
	mustRegisterGo(t, checker, &value)

	if err := checker.CheckAfter(); err != nil {
		t.Fatalf("empty C table unexpectedly failed: %v", err)
	}

	*(*unsafe.Pointer)(blockPtr) = unsafe.Pointer(&value)
	err := mustFailAfter(t, checker)
	if got := err.Violations[0].Field; got != "slot" {
		t.Fatalf("C block violation field = %q, want slot", got)
	}
}

func TestCBlockTypeLayout(t *testing.T) {
	type cMirror struct {
		Pointer unsafe.Pointer
		Number  int64
	}

	blockMemory := make([]byte, 64)
	blockPtr := unsafe.Pointer(&blockMemory[0])
	checker := New()
	checker.RegisterCBlock(blockPtr, uintptr(len(blockMemory)), TypeLayout(reflect.TypeOf(cMirror{})))

	value := 29
	mustRegisterGo(t, checker, &value)
	*(*unsafe.Pointer)(blockPtr) = unsafe.Pointer(&value)

	mustFailAfter(t, checker)
}

func TestUnregisteredArgumentAndCPointerArgument(t *testing.T) {
	value := 31
	checker := New()
	mustFailBefore(t, checker, unsafe.Pointer(&value))

	blockMemory := make([]byte, 32)
	blockPtr := unsafe.Pointer(&blockMemory[0])
	checker.RegisterCBlock(blockPtr, uintptr(len(blockMemory)), PtrField("slot", 0))
	mustPassBefore(t, checker, blockPtr)
}

func TestUintptrIsNotTreatedAsPointer(t *testing.T) {
	value := 37
	s := struct {
		Address uintptr
	}{
		Address: uintptr(unsafe.Pointer(&value)),
	}

	checker := New()
	mustRegisterGo(t, checker, &s)
	mustPassBefore(t, checker, unsafe.Pointer(&s))
}

func TestPureByteArrayIgnoresPointerShapedData(t *testing.T) {
	value := 41
	raw := [uintptr(unsafe.Sizeof(uintptr(0)))]byte{}
	*(*unsafe.Pointer)(unsafe.Pointer(&raw[0])) = unsafe.Pointer(&value)

	checker := New()
	mustRegisterGo(t, checker, &raw)
	mustPassBefore(t, checker, unsafe.Pointer(&raw[0]))
}

func TestCBlockMayRetainAnotherCBlock(t *testing.T) {
	targetMemory := make([]byte, 32)
	target := unsafe.Pointer(&targetMemory[0])

	holderMemory := make([]byte, 32)
	holder := unsafe.Pointer(&holderMemory[0])

	checker := New()
	checker.RegisterCBlock(target, uintptr(len(targetMemory)), PtrField("slot", 0))
	checker.RegisterCBlock(holder, uintptr(len(holderMemory)), PtrField("target", 0))
	*(*unsafe.Pointer)(holder) = target

	if err := checker.CheckAfter(); err != nil {
		t.Fatalf("C-to-C retention unexpectedly failed: %v", err)
	}
}

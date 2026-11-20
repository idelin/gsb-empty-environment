package cgocheck

import (
	"fmt"
	"strings"
)

// ViolationKind identifies the pointer rule that was broken.
type ViolationKind string

const (
	// ViolationGoPointerInGoMemory means that memory passed to C contains a
	// word which refers into Go memory.
	ViolationGoPointerInGoMemory ViolationKind = "go pointer in Go memory passed to C"

	// ViolationUnknownPointer means that a pointer word in memory passed to C
	// refers to memory whose provenance was never registered.
	ViolationUnknownPointer ViolationKind = "unregistered pointer in memory passed to C"

	// ViolationUnregisteredArgument means that the pointer passed as a C
	// argument is neither in a registered Go allocation nor in a registered
	// C block.
	ViolationUnregisteredArgument ViolationKind = "unregistered pointer passed to C"

	// ViolationRetainedGoPointer means that C retained a pointer into Go
	// memory after the call returned.
	ViolationRetainedGoPointer ViolationKind = "Go pointer retained by C"
)

// Violation describes one failed pointer check.
type Violation struct {
	Kind ViolationKind

	// Pointer is the address of the Go pointer supplied as an argument, or the
	// address of the slot in which a retained pointer was found.
	Pointer uintptr

	// Target is the non-nil pointer value that failed classification.
	Target uintptr

	// Object and Field identify the registered allocation and, when available,
	// the field path involved in the violation.
	Object string
	Field  string

	Message string
}

func (v Violation) Error() string {
	var b strings.Builder
	b.WriteString(string(v.Kind))
	if v.Message != "" {
		b.WriteString(": ")
		b.WriteString(v.Message)
	}
	if v.Pointer != 0 {
		fmt.Fprintf(&b, " (pointer slot 0x%x", v.Pointer)
		if v.Target != 0 {
			fmt.Fprintf(&b, ", target 0x%x", v.Target)
		}
		if v.Object != "" {
			fmt.Fprintf(&b, ", object %s", v.Object)
		}
		if v.Field != "" {
			fmt.Fprintf(&b, ", field %s", v.Field)
		}
		b.WriteByte(')')
	}
	return b.String()
}

// RuleError is returned when one or more pointer-rule violations are found.
type RuleError struct {
	Violations []Violation
}

func (e *RuleError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return ""
	}
	if len(e.Violations) == 1 {
		return e.Violations[0].Error()
	}
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = v.Error()
	}
	return fmt.Sprintf("%d pointer rule violations: %s", len(parts), strings.Join(parts, "; "))
}

func ruleError(violations []Violation) error {
	if len(violations) == 0 {
		return nil
	}
	return &RuleError{Violations: violations}
}

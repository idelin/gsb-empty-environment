package engine

import (
	"strconv"
	"strings"
)

// ErrCode identifies the kind of a formula error.
type ErrCode string

const (
	// ErrRef is produced when a formula references a cell that does not exist.
	ErrRef ErrCode = "#REF!"
	// ErrCycle is produced when executed dependencies form a cycle.
	ErrCycle ErrCode = "#CYCLE!"
	// ErrLiteral is the explicit error literal #ERR!.
	ErrLiteral ErrCode = "#ERR!"
)

// Value is the result of evaluating a cell: either an integer or an error.
type Value struct {
	IsErr bool
	Num   int
	Code  ErrCode
	// Cycle holds the cycle evidence (cell ids in dependency order) when
	// Code == ErrCycle. The last cell depends on the first one again.
	Cycle []string
}

// Int returns an integer value.
func Int(n int) Value { return Value{Num: n} }

// Err returns an error value with the given code.
func Err(code ErrCode) Value { return Value{IsErr: true, Code: code} }

// CycleErr returns a cycle error carrying the given evidence.
func CycleErr(cycle []string) Value {
	return Value{IsErr: true, Code: ErrCycle, Cycle: cycle}
}

func (v Value) String() string {
	if !v.IsErr {
		return strconv.Itoa(v.Num)
	}
	if v.Code == ErrCycle && len(v.Cycle) > 0 {
		parts := append([]string{}, v.Cycle...)
		parts = append(parts, v.Cycle[0])
		return string(v.Code) + " (" + strings.Join(parts, " -> ") + ")"
	}
	return string(v.Code)
}

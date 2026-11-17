package netsec

import "fmt"

// Point is one syscall invocation in a finite verification domain.
type Point struct {
	Syscall uint32
	Args    [6]uint64
}

// Enumerate walks the Cartesian product of syscalls and six argument domains.
// A nil or empty domain is treated as the single value zero. Returning false
// from visit stops the enumeration.
func Enumerate(syscalls []uint32, domains [6][]uint64, visit func(Point) bool) {
	if visit == nil {
		return
	}

	var args [6]uint64
	for _, syscall := range syscalls {
		var rec func(int) bool
		rec = func(index int) bool {
			if index == 6 {
				return visit(Point{Syscall: syscall, Args: args})
			}
			values := domains[index]
			if len(values) == 0 {
				values = []uint64{0}
			}
			for _, value := range values {
				args[index] = value
				if !rec(index + 1) {
					args[index] = 0
					return false
				}
			}
			args[index] = 0
			return true
		}
		if !rec(0) {
			return
		}
	}
}

// NarrowingError reports a point where an appended chain became less
// restrictive than the chain it replaced.
type NarrowingError struct {
	Point  Point
	Before Decision
	After  Decision
}

func (e *NarrowingError) Error() string {
	return fmt.Sprintf(
		"netsec: chain widened at syscall %d args %v: %s -> %s",
		e.Point.Syscall, e.Point.Args, e.Before, e.After,
	)
}

// VerifyNarrowing proves over a finite domain that after is never less
// restrictive than before. It is intended for host self-tests and audit
// tooling that can afford exhaustive enumeration.
func VerifyNarrowing(before, after *Chain, syscalls []uint32, domains [6][]uint64) error {
	var violation *NarrowingError
	Enumerate(syscalls, domains, func(point Point) bool {
		oldDecision := before.Evaluate(point.Syscall, point.Args)
		newDecision := after.Evaluate(point.Syscall, point.Args)
		if newDecision.Action < oldDecision.Action {
			violation = &NarrowingError{
				Point:  point,
				Before: oldDecision,
				After:  newDecision,
			}
			return false
		}
		return true
	})
	if violation != nil {
		return violation
	}
	return nil
}

// VerifyMonotone and VerifyAppendOnlyNarrows are aliases for VerifyNarrowing.
func VerifyMonotone(before, after *Chain, syscalls []uint32, domains [6][]uint64) error {
	return VerifyNarrowing(before, after, syscalls, domains)
}

// VerifyAppendOnlyNarrows is an alias for VerifyNarrowing.
func VerifyAppendOnlyNarrows(before, after *Chain, syscalls []uint32, domains [6][]uint64) error {
	return VerifyNarrowing(before, after, syscalls, domains)
}

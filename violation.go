package netsec

import (
	"errors"
	"fmt"
)

// ViolationKind identifies the kind of unsafe conversion that was rejected.
type ViolationKind string

const (
	// KindOutOfBounds means the target starts outside, or extends past, the
	// allocation that owns the source pointer.
	KindOutOfBounds ViolationKind = "out_of_bounds"
	// KindCrossAllocation means the target range crosses from the source
	// allocation into another registered allocation (or into unregistered
	// memory).
	KindCrossAllocation ViolationKind = "cross_allocation"
	// KindUnregistered means no allocation containing the source pointer was
	// registered with the checker.
	KindUnregistered ViolationKind = "unregistered"
	// KindMisaligned means the target address is not aligned for its type.
	KindMisaligned ViolationKind = "misaligned"
	// KindLengthOverflow means a length, element-count, or address computation
	// would overflow uintptr.
	KindLengthOverflow ViolationKind = "length_overflow"
	// KindNegativeLength means a negative attacker-controlled length was
	// supplied.
	KindNegativeLength ViolationKind = "negative_length"
	// KindInvalidLayout means the supplied size/alignment description is not
	// a valid Go type layout.
	KindInvalidLayout ViolationKind = "invalid_layout"
	// KindStaleUintptr means a uintptr obtained from a pointer was converted
	// back to a pointer after at least one registered GC safe point.
	KindStaleUintptr ViolationKind = "stale_uintptr"
	// KindUntrackedUintptr means a uintptr was converted to a pointer without
	// first being registered with PointerToUintptr. Such a value has unknown
	// provenance and cannot be proven safe across GC safe points.
	KindUntrackedUintptr ViolationKind = "untracked_uintptr"
)

// Compatibility aliases for the violation kinds.
const (
	OutOfBounds               = KindOutOfBounds
	CrossAllocation           = KindCrossAllocation
	Unregistered              = KindUnregistered
	Misaligned                = KindMisaligned
	LengthOverflow            = KindLengthOverflow
	NegativeLength            = KindNegativeLength
	InvalidLayout             = KindInvalidLayout
	StaleUintptr              = KindStaleUintptr
	UntrackedUintptr          = KindUntrackedUintptr
	ViolationOutOfBounds      = KindOutOfBounds
	ViolationCrossAllocation  = KindCrossAllocation
	ViolationUnregistered     = KindUnregistered
	ViolationMisaligned       = KindMisaligned
	ViolationLengthOverflow   = KindLengthOverflow
	ViolationNegativeLength   = KindNegativeLength
	ViolationInvalidLayout    = KindInvalidLayout
	ViolationStaleUintptr     = KindStaleUintptr
	ViolationUntrackedUintptr = KindUntrackedUintptr
)

// Sentinel errors for errors.Is checks. A *Violation matches the sentinel for
// its kind.
var (
	ErrOutOfBounds      = errors.New("netsec: target is out of bounds")
	ErrCrossAllocation  = errors.New("netsec: target crosses an allocation boundary")
	ErrUnregistered     = errors.New("netsec: pointer belongs to no registered allocation")
	ErrMisaligned       = errors.New("netsec: target is not aligned for its type")
	ErrLengthOverflow   = errors.New("netsec: length or address computation overflowed")
	ErrNegativeLength   = errors.New("netsec: negative length")
	ErrInvalidLayout    = errors.New("netsec: invalid type layout")
	ErrStaleUintptr     = errors.New("netsec: uintptr crossed a GC safe point")
	ErrUntrackedUintptr = errors.New("netsec: uintptr has no recorded pointer provenance")
)

// Violation is returned (and recorded by the Checker) whenever a conversion
// cannot be proven memory safe.
type Violation struct {
	Kind       ViolationKind
	Op         string
	Address    uintptr
	Offset     uintptr
	Length     uintptr
	Target     Layout
	Allocation *Allocation
	Epoch      uint64
	Detail     string
}

// Error implements the error interface.
func (v *Violation) Error() string {
	where := ""
	if v.Address != 0 {
		where = fmt.Sprintf(" at 0x%x", v.Address)
	}
	if v.Offset != 0 {
		where += fmt.Sprintf(" offset=0x%x", v.Offset)
	}
	if v.Length != 0 {
		where += fmt.Sprintf(" length=0x%x", v.Length)
	}
	if v.Detail != "" {
		return fmt.Sprintf("netsec: %s: %s%s: %s", v.Op, v.Kind, where, v.Detail)
	}
	return fmt.Sprintf("netsec: %s: %s%s", v.Op, v.Kind, where)
}

// Is lets errors.Is(err, ErrStaleUintptr) and similar checks work.
func (v *Violation) Is(target error) bool {
	switch target {
	case ErrOutOfBounds:
		return v.Kind == KindOutOfBounds
	case ErrCrossAllocation:
		return v.Kind == KindCrossAllocation
	case ErrUnregistered:
		return v.Kind == KindUnregistered
	case ErrMisaligned:
		return v.Kind == KindMisaligned
	case ErrLengthOverflow:
		return v.Kind == KindLengthOverflow
	case ErrNegativeLength:
		return v.Kind == KindNegativeLength
	case ErrInvalidLayout:
		return v.Kind == KindInvalidLayout
	case ErrStaleUintptr:
		return v.Kind == KindStaleUintptr
	case ErrUntrackedUintptr:
		return v.Kind == KindUntrackedUintptr
	}
	return false
}

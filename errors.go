package packetvm

import "fmt"

// RejectKind categorises why a tenant program was refused.
type RejectKind uint8

const (
	// RejectUnverifiedLoad: a packet read is reachable on a path that has
	// not proven the packet is long enough to contain it.
	RejectUnverifiedLoad RejectKind = iota + 1
	// RejectBadOffset: an offset/size combination overflows the address
	// space or uses an unsupported width.
	RejectBadOffset
	// RejectBackwardJump: a jump targets an earlier instruction, which the
	// forward-only machine forbids.
	RejectBackwardJump
	// RejectDanglingJump: a jump targets beyond the program.
	RejectDanglingJump
	// RejectEmptyProgram: the rule produced no terminating instruction.
	RejectEmptyProgram
	// RejectTooLarge: the program exceeds the instruction budget.
	RejectTooLarge
	// RejectInvalidAST: the source tree is malformed.
	RejectInvalidAST
)

func (k RejectKind) String() string {
	switch k {
	case RejectUnverifiedLoad:
		return "unverified packet load"
	case RejectBadOffset:
		return "invalid load offset or size"
	case RejectBackwardJump:
		return "backward jump"
	case RejectDanglingJump:
		return "jump targets end of program"
	case RejectEmptyProgram:
		return "empty program"
	case RejectTooLarge:
		return "program too large"
	case RejectInvalidAST:
		return "invalid rule expression"
	default:
		return fmt.Sprintf("reject(%d)", uint8(k))
	}
}

// RejectError is returned when a rule cannot be proven safe. It carries the
// originating source expression so callers can point tenants at the exact
// clause responsible.
type RejectError struct {
	Kind       RejectKind
	Source     Source
	Index      int    // instruction index, -1 when not instruction-bound
	Need       uint32 // bytes that must be verified (load checks)
	Guaranteed uint32 // bytes guaranteed on every reaching path
	Detail     string
}

func (e *RejectError) Error() string {
	loc := e.Source.String()
	if e.Index >= 0 {
		loc = fmt.Sprintf("%s at instruction %d", loc, e.Index)
	}
	switch e.Kind {
	case RejectUnverifiedLoad:
		return fmt.Sprintf("%s: %s requires %d verified bytes but only %d are guaranteed on every reaching path",
			e.Kind, loc, e.Need, e.Guaranteed)
	default:
		if e.Detail != "" {
			return fmt.Sprintf("%s: %s: %s", e.Kind, loc, e.Detail)
		}
		return fmt.Sprintf("%s: %s", e.Kind, loc)
	}
}

// AsReject extracts a *RejectError from an error, if present.
func AsReject(err error) (*RejectError, bool) {
	re, ok := err.(*RejectError)
	return re, ok
}

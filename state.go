package netsec

import (
	"fmt"
	"time"
)

// Credential is the authorization secret bound to one region.  Using a string
// alias keeps call sites simple while documenting the value's role.
type Credential = string

// RegionState is the durable lifecycle state of a persistent-memory region.
//
// A region may only be handed to a new tenant while it is StateClean.  The
// three states are intentionally exhaustive: an authorization lockout is a
// volatile security control (see Status.Locked), not a fourth region state.
type RegionState uint8

const (
	// StateInUse means the region is leased to a tenant and may contain data
	// that must not be exposed to another tenant.
	StateInUse RegionState = iota + 1
	// StateErasing means an authorized erase has started but has not yet
	// completed and been verified.  It is never allocatable.
	StateErasing
	// StateClean means every stripe has been overwritten, read back and
	// verified.  Only this state is allocatable.
	StateClean
)

// Short aliases for the three lifecycle states.
const (
	InUse   = StateInUse
	Erasing = StateErasing
	Clean   = StateClean
)

func (s RegionState) String() string {
	switch s {
	case StateInUse:
		return "in-use"
	case StateErasing:
		return "erasing"
	case StateClean:
		return "clean"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(s))
	}
}

// Allocatable reports whether the state permits a new lease.  Erasing and
// in-use regions are deliberately not allocatable.
func (s RegionState) Allocatable() bool {
	return s == StateClean
}

// ParseRegionState converts the textual form used by Status and journal
// tooling back into a RegionState.
func ParseRegionState(s string) (RegionState, error) {
	switch s {
	case "in-use":
		return StateInUse, nil
	case "erasing":
		return StateErasing, nil
	case "clean":
		return StateClean, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidState, s)
	}
}

// Lease identifies the successful allocation of a clean region to a tenant.
type Lease struct {
	RegionID    string
	Tenant      string
	AllocatedAt time.Time
}

// Status is an immutable snapshot of a region and its volatile security
// controls.
type Status struct {
	RegionID           string
	Size               int64
	State              RegionState
	Tenant             string
	AllocatedAt        time.Time
	TotalStripes       int64
	VerifiedStripes    int64
	Locked             bool
	FailedAuthAttempts int
}

// Allocatable is a convenience helper for Status.
func (s Status) Allocatable() bool {
	return s.State.Allocatable()
}

// Frozen reports whether authentication failures have locked the region until
// a process restart.  It is a volatile control, not a RegionState.
func (s Status) Frozen() bool {
	return s.Locked
}

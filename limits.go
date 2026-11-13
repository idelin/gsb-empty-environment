// Package netsec implements the job-object restriction policy used by the
// sandbox host. It combines Windows job limits (active process cap, committed
// memory cap, KILL_ON_JOB_CLOSE, the two breakaway allows and UI restriction
// bits) with the admission logic applied before a process is created.
//
// The policy engine in this file is platform independent so it can be unit
// tested off Windows; job_windows.go provides the concrete kernel job object
// and CreateProcess wrapper used by the real host.
package netsec

import "errors"

// UIRestrictions mirrors the JOB_OBJECT_UILIMIT_* bitmask. A set bit denies the
// corresponding user-interface capability to every process in the job.
type UIRestrictions uint32

const (
	UILimitHandles          UIRestrictions = 0x0001 // JOB_OBJECT_UILIMIT_HANDLES
	UILimitReadClipboard    UIRestrictions = 0x0002 // JOB_OBJECT_UILIMIT_READCLIPBOARD
	UILimitWriteClipboard   UIRestrictions = 0x0004 // JOB_OBJECT_UILIMIT_WRITECLIPBOARD
	UILimitSystemParameters UIRestrictions = 0x0008 // JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS
	UILimitDisplaySettings  UIRestrictions = 0x0010 // JOB_OBJECT_UILIMIT_DISPLAYSETTINGS
	UILimitGlobalAtoms      UIRestrictions = 0x0020 // JOB_OBJECT_UILIMIT_GLOBALATOMS
	UILimitDesktop          UIRestrictions = 0x0040 // JOB_OBJECT_UILIMIT_DESKTOP
	UILimitExitWindows      UIRestrictions = 0x0080 // JOB_OBJECT_UILIMIT_EXITWINDOWS
)

// Limits is the platform-neutral representation of a job object restriction
// set. A zero MaxActiveProcesses or MaxJobMemory means the corresponding cap
// is not enforced.
type Limits struct {
	// MaxActiveProcesses is the JOB_OBJECT_LIMIT_ACTIVE_PROCESS cap.
	MaxActiveProcesses uint32
	// MaxJobMemory is the JOB_OBJECT_LIMIT_JOB_MEMORY committed-memory cap.
	MaxJobMemory uint64
	// KillOnJobClose maps to JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: every
	// surviving process is terminated when the last job handle closes.
	KillOnJobClose bool
	// BreakawayOK maps to JOB_OBJECT_LIMIT_BREAKAWAY_OK and permits children
	// created with CREATE_BREAKAWAY_FROM_JOB to leave the job.
	BreakawayOK bool
	// SilentBreakawayOK maps to JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK: children
	// are not assigned to the job at all.
	SilentBreakawayOK bool
	// UI is the JOB_OBJECT_UILIMIT_* restriction bitmask.
	UI UIRestrictions
}

var (
	// ErrBreakawayDenied is returned when a process requests an explicit
	// breakaway but the job does not grant JOB_OBJECT_LIMIT_BREAKAWAY_OK.
	ErrBreakawayDenied = errors.New("netsec: CREATE_BREAKAWAY_FROM_JOB denied: JOB_OBJECT_LIMIT_BREAKAWAY_OK not set")
	// ErrSilentBreakawayDenied is returned when CREATE_SILENT_BREAKAWAY_FROM_JOB
	// is requested without JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK.
	ErrSilentBreakawayDenied = errors.New("netsec: CREATE_SILENT_BREAKAWAY_FROM_JOB denied: JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK not set")
	// ErrProcessLimitExceeded is returned when creating another member would
	// exceed MaxActiveProcesses. The request is refused; existing processes
	// are never killed to make room.
	ErrProcessLimitExceeded = errors.New("netsec: active process limit exceeded")
	// ErrKillOnCloseDowngrade is returned when a child job tries to clear
	// KILL_ON_JOB_CLOSE that a parent job already enforces.
	ErrKillOnCloseDowngrade = errors.New("netsec: child job cannot clear KILL_ON_JOB_CLOSE enforced by parent")
)

// SandboxLimits returns the mandatory sandbox baseline: KILL_ON_JOB_CLOSE is
// enabled and both breakaway allows are disabled so no child process can escape
// the job. Numeric caps and UI bits are supplied by the caller and preserved.
func SandboxLimits(caps Limits) Limits {
	caps.KillOnJobClose = true
	caps.BreakawayOK = false
	caps.SilentBreakawayOK = false
	return caps
}

// ValidateSandboxPolicy rejects a limit set that re-enables either breakaway
// path or drops KILL_ON_JOB_CLOSE.
func ValidateSandboxPolicy(limits Limits) error {
	if limits.BreakawayOK {
		return ErrBreakawayDenied
	}
	if limits.SilentBreakawayOK {
		return ErrSilentBreakawayDenied
	}
	if !limits.KillOnJobClose {
		return ErrKillOnCloseDowngrade
	}
	return nil
}

// MergeLimits combines a parent job's effective limits with a child job's own
// limits using the stricter value for every field:
//
//   - Numeric caps take the smaller non-zero value (0 means unlimited).
//   - KillOnJobClose and the UI restriction bits are restrictions, so they
//     accumulate (boolean OR / bitwise OR).
//   - BreakawayOK and SilentBreakawayOK are escape capabilities rather than
//     restrictions: the effective value is the boolean AND, so a nested job
//     can never re-enable an escape the parent closed.
func MergeLimits(parent, child Limits) Limits {
	return Limits{
		MaxActiveProcesses: minNonZero(parent.MaxActiveProcesses, child.MaxActiveProcesses),
		MaxJobMemory:       minNonZero(parent.MaxJobMemory, child.MaxJobMemory),
		KillOnJobClose:     parent.KillOnJobClose || child.KillOnJobClose,
		BreakawayOK:        parent.BreakawayOK && child.BreakawayOK,
		SilentBreakawayOK:  parent.SilentBreakawayOK && child.SilentBreakawayOK,
		UI:                 parent.UI | child.UI,
	}
}

// ValidateNesting checks whether child may be nested under parent. Windows does
// not allow a child job to relax a restriction already imposed by its parent;
// in particular KILL_ON_JOB_CLOSE, once set on a parent, cannot be cleared on
// the child.
func ValidateNesting(parent, child Limits) error {
	if parent.KillOnJobClose && !child.KillOnJobClose {
		return ErrKillOnCloseDowngrade
	}
	return nil
}

// NestedLimits validates the parent/child relationship and returns the
// effective limits of the nested job. It returns ErrKillOnCloseDowngrade if
// the child attempts to drop the parent's KILL_ON_JOB_CLOSE guarantee.
func NestedLimits(parent, child Limits) (Limits, error) {
	if err := ValidateNesting(parent, child); err != nil {
		return Limits{}, err
	}
	return MergeLimits(parent, child), nil
}

func minNonZero[T ~uint32 | ~uint64](a, b T) T {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

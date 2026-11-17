package netsec

import "fmt"

// Action is the result of evaluating one syscall against a filter. The values
// are ordered from least to most restrictive.
type Action uint8

const (
	// ActionAllow lets the syscall run.
	ActionAllow Action = iota
	// ActionNotify asks a user-space supervisor to decide about the syscall.
	ActionNotify
	// ActionDeny rejects the syscall with the decision's errno data.
	ActionDeny
	// ActionTerminate stops the process.
	ActionTerminate
)

// Short aliases for callers that prefer an action-as-value style.
const (
	Allow     = ActionAllow
	Notify    = ActionNotify
	Deny      = ActionDeny
	Terminate = ActionTerminate
	Kill      = ActionTerminate
)

// Common names used by seccomp hosts.
const (
	ActionKill        = ActionTerminate
	ActionKillProcess = ActionTerminate
	ActionKillThread  = ActionTerminate
	ActionErrno       = ActionDeny
	ActionUserNotify  = ActionNotify
)

// Valid reports whether a is one of the four defined actions.
func (a Action) Valid() bool {
	return a <= ActionTerminate
}

// MoreRestrictiveThan reports whether a has a higher priority than other.
func (a Action) MoreRestrictiveThan(other Action) bool {
	return a > other
}

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "allow"
	case ActionNotify:
		return "notify"
	case ActionDeny:
		return "deny"
	case ActionTerminate:
		return "terminate"
	default:
		return fmt.Sprintf("Action(%d)", uint8(a))
	}
}

// Decision is the pure result of a filter for one syscall invocation. Data is
// action-specific (for example, an errno for ActionDeny or a signal number for
// ActionTerminate).
type Decision struct {
	Action Action
	Data   uint32
}

// Verdict and Result are aliases kept for hosts that use different wording.
type (
	Verdict = Decision
	Result  = Decision
)

func (d Decision) IsAllow() bool     { return d.Action == ActionAllow }
func (d Decision) IsNotify() bool    { return d.Action == ActionNotify }
func (d Decision) IsDeny() bool      { return d.Action == ActionDeny }
func (d Decision) IsTerminate() bool { return d.Action == ActionTerminate }

func (d Decision) String() string {
	return fmt.Sprintf("%s(data=%d)", d.Action, d.Data)
}

// ComposeDecisions combines an older decision with a newer one.
//
// The more restrictive action always wins. If both actions have the same
// priority, the newer decision supplies the data. Consequently, appending a
// filter can never turn a terminate, deny, or notify decision into an allow.
func ComposeDecisions(older, newer Decision) Decision {
	switch {
	case newer.Action > older.Action:
		return newer
	case newer.Action < older.Action:
		return older
	default:
		// Equal priority: the most recently installed filter owns the data.
		return newer
	}
}

// Package netsec builds reset coverage proofs for device registers that may
// retain tenant data.
//
// The secret state of a register is described bitwise. Each bit is tracked in
// an abstract domain:
//   - BitUnknown: the bit is not known to be zero and may still hold a secret;
//   - BitZero: the bit is deterministically zero, i.e. proven clean.
//
// A reset Action clears a given bit mask and may, as a side effect, make
// other bits unknown again. Actions carry a RequireZero precondition mask and
// are only legal when every bit named by that mask is already BitZero.
//
// PlanReset and PlanResetFrom run an exhaustive full-state breadth-first
// search (bounded by MaxActions) over the abstract register state, so the
// returned Plan is provably the shortest legal sequence: every transition is
// checked against the action preconditions, ErrUnclearable is only returned
// after the whole reachable state space has been exhausted, and
// ErrLimitExceeded reports a target that needs more than MaxActions steps.
package netsec

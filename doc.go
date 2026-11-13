// Package netsec implements social recovery for a Go identity host.
//
// A Host stores immutable recovery configuration snapshots, signed contact
// approvals, cooling state, vetoes, and completion markers in one atomic
// AccountState commit per operation. Contacts are counted by person and key
// equivalence groups, each group contributing at most its largest approved
// weight. A recovery moves to Cooling when both the weight threshold and the
// minimum distinct-person count are met; credential rotation is delayed until
// the cooling period ends and no unrevoked strong factor has vetoed.
//
// FindMinimalDivergence compares CorrectPolicy with explicit pending
// implementations that omit dedup or epoch callbacks.
package netsec

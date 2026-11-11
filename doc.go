// Package netsec correlates time-ordered security events into confirmed
// attack chains while enforcing single event consumption: each event belongs
// to at most one confirmed chain, preventing duplicate escalation when the
// alert engine matches one event against several candidate chains.
//
// Assign solves a maximum set-packing over complete chain instances. It first
// maximizes the number of complete chains, then minimizes total time span,
// then breaks remaining ties by the lexicographically smallest ordered
// event-ID assignment. A bounded branch-and-bound search with state
// memoization handles up to MaxEvents events; when the work or wall-clock
// budget is exhausted it returns a verified feasible incumbent without
// claiming optimality.
package netsec

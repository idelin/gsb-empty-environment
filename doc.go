// Package csp implements a small finite-domain integer constraint solver.
//
// Variables hold finite integer domains. Supported constraints are
// equality (x == y, x == c), difference bounds (x - y <= c), and
// all-different. After every domain update, constraints are propagated
// to a fixpoint. Propagation is conservative: it never removes a value
// that participates in a real solution, so a domain emptied by
// propagation proves unsatisfiability. The converse does not hold:
// propagation reaching a fixpoint without failure does NOT imply a
// solution exists. Unsatisfiability in general is confirmed only by
// complete backtracking search.
//
// Domains can be saved with Snapshot and rolled back with Restore,
// which the backtracking search uses internally and which callers may
// use for their own search strategies.
package csp

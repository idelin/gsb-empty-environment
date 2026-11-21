// Package recovery plans a restart group for stateful workload migration.
//
// Each physical volume is a rooted checkpoint tree.  A node's Marks and Blocks
// belong to the indivisible log segment entering that node; the root's fields
// describe its base state.  A mounted candidate therefore accumulates marks and
// content blocks along root -> node.
//
// # Commit rule
//
// Exactly one candidate is selected for every logical volume, and no physical
// volume may be selected twice.  For a transaction t and every logical volume
// v, let own be the number of accumulated marks (t, v) and wrong the number of
// accumulated marks (t, w) with w != v.  The transaction is committed exactly
// when, for every v:
//
//	if v participates in t: own == 1 and wrong == 0
//	otherwise:              own == 0 and wrong == 0
//
// Thus a wrong-volume mark, a duplicate mark, or a missing participant mark
// all prevent commitment.  A transaction that is not committed contributes no
// benefit, but does not by itself invalidate a combination.
//
// A legal combination commits every caller-required transaction and is closed
// under prerequisites: if t is committed, every prerequisite of t is committed.
// Among legal combinations the planner first maximizes the sum of signed
// transaction benefits and then minimizes the cost of the union of content
// blocks referenced by the selected candidates.  Costs and counts are exact
// math/big integers.
//
// # Ranked certificates
//
// Plan returns the number of optimal combinations and a mount certificate for
// a requested 1-based rank.  Combinations are ordered lexicographically by
// logical-volume index and then by candidate input order.  Certificate.Confirm
// re-reads all selected physical volumes, volume generations, node generations
// and snapshot digests.  It returns a non-nil choice group only if every
// observation matches, so a host must not publish any part of a group when
// Confirm returns an error.
//
// # Retained boundary state
//
// Logical volumes are processed in input order.  A transaction is resident from
// just before its first possible participant/mark check until all direct
// dependents have closed.  For each resident transaction the state retains:
//
//   - one viability bit (no wrong/duplicate/missing mark seen), and
//   - one must-commit bit (required by the caller or by a committed dependent).
//
// Dependencies themselves are not retained across the cut; they are reduced to
// the must-commit bit.  A prerequisite is kept resident until its direct
// dependents close, so it cannot close before a dependent that might require
// it.
//
// In addition, state retains one used bit for each physical volume that is
// referenced by candidates on both sides of the cut, and one downloaded bit for
// each content block referenced on both sides of the cut.  Objects that cannot
// be referenced again are dropped immediately.  The default limit on the total
// number of these retained items per layer is DefaultMaxBoundaryItems (10); it
// can be raised with Input.MaxBoundaryItems.  Exceeding the limit returns
// ErrBoundaryWidth.  The DP is bounded by frontier states, never by the total
// number of combinations (up to 4^60).
//
// The package supports at most 60 logical volumes and at most four candidates
// per logical volume.
package recovery

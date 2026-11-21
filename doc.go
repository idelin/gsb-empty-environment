// Package indexvis computes exact snapshot-visibility bounds for a compressed
// index page while the commit table is still incomplete.
//
// The input is a Snapshot, a set of dependency edges u -> v ("if u commits,
// v commits"), and a PageSummary that compresses versions by their
// (creator, deleter) transaction pair. Transactions above the snapshot bound
// or active in the snapshot are not yet effective: they are never visible, but
// their eventual commit/abort state still participates in dependency
// implications. Other transactions may be known committed, known aborted, or
// unknown.
//
// A version is visible exactly when its creator is effective and its deleter
// is not effective. NoTransaction as the deleter means that there is no
// deleting transaction.
//
// NewIndex builds a tree decomposition whose bags contain at most MaxBagSize
// (11) transactions and whose bags cover every dependency edge and every
// creator/deleter pair. Evaluate then runs a joint min/max dynamic program over
// that decomposition. The state space is 2^11 per bag, independent of the
// global number of transactions, so the implementation never enumerates global
// completions. Because the DP is joint, a transaction that creates a version in
// one page or subtree and deletes a version in another is assigned one shared
// outcome.
//
// Index supports repeated commit-table completion and local multiplicity
// changes on the fixed decomposition. SetStatus and AddVersions return an
// UpdateProof listing precisely the affected bags' paths to the root and the
// number of table/projection entries recomputed.
//
// Known committed-to-aborted dependency paths are reported as
// ContradictionError. SetStatus rolls a contradictory change back before
// returning the error, leaving the index usable.
package indexvis
